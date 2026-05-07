package goagent

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

const defaultMaxSteps = 32

var nextRunID atomic.Int64

// NewRunner builds a Runner from an Agent definition.
func NewRunner(agent Agent) (Runner, error) {
	if agent.Model == nil {
		return nil, fmt.Errorf("goagent: agent model is required")
	}

	tools, specs, err := buildToolRegistry(agent.Tools)
	if err != nil {
		return nil, err
	}

	for _, sink := range agent.EventSinks {
		if sink == nil {
			return nil, fmt.Errorf("goagent: agent event sink cannot be nil")
		}
	}
	if agent.Retry.MaxAttempts < 0 {
		return nil, fmt.Errorf("goagent: retry max attempts cannot be negative")
	}
	if agent.Retry.Delay < 0*time.Second {
		return nil, fmt.Errorf("goagent: retry delay cannot be negative")
	}

	policy := agent.Policy
	policyExplicit := true
	if policy == nil {
		policy = allowAllPolicy{}
		policyExplicit = false
	}

	return &runner{
		instructions:   agent.Instructions,
		model:          agent.Model,
		tools:          tools,
		toolSpecs:      specs,
		policy:         policy,
		policyExplicit: policyExplicit,
		sessionStore:   agent.SessionStore,
		eventSinks:     append([]EventSink(nil), agent.EventSinks...),
		retry:          agent.Retry,
	}, nil
}

type runner struct {
	instructions   string
	model          Model
	tools          map[string]registeredTool
	toolSpecs      []ToolSpec
	policy         Policy
	policyExplicit bool
	sessionStore   SessionStore
	eventSinks     []EventSink
	retry          RetryPolicy
}

func (r *runner) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	return r.run(ctx, request, func(Event) {})
}

func (r *runner) Stream(ctx context.Context, request RunRequest) (<-chan Event, error) {
	events := make(chan Event)
	go func() {
		defer close(events)
		_, _ = r.run(ctx, request, func(event Event) {
			events <- event
		})
	}()
	return events, nil
}

func (r *runner) run(ctx context.Context, request RunRequest, emit func(Event)) (RunResult, error) {
	session := requestSession(request)
	state := runState{
		runID:   fmt.Sprintf("run-%d", nextRunID.Add(1)),
		ctx:     ctx,
		request: request,
		session: session,
		emit:    emit,
		sinks:   r.eventSinks,
	}
	loaded, stopReason, err := r.loadSession(ctx, &state, request)
	if err != nil {
		if r.retry.MaxAttempts <= 1 {
			return RunResult{}, err
		}
		return state.fail(stopReason, eventPayload{err: err}), err
	}
	state.session = loaded
	if request.Input != "" {
		state.session.Messages = append(state.session.Messages, textMessage(RoleUser, request.Input))
	}

	maxSteps := request.MaxSteps
	if maxSteps == 0 {
		maxSteps = defaultMaxSteps
	}
	runDecision, err := r.decide(ctx, &state, "", Decision{Kind: DecisionRunStart, Request: request, Session: state.session})
	if err != nil {
		return r.saveResult(ctx, state.fail(StopPolicyDenied, eventPayload{err: err}))
	}
	if !runDecision.Allowed {
		return r.saveResult(ctx, r.finish(ctx, &state, "", StopPolicyDenied))
	}
	if runDecision.MaxSteps > 0 && runDecision.MaxSteps < maxSteps {
		maxSteps = runDecision.MaxSteps
	}

	for step := 0; ; step++ {
		if err := ctx.Err(); err != nil {
			return r.saveResult(ctx, state.fail(StopCanceled, eventPayload{err: err}))
		}
		if step >= maxSteps {
			return r.saveResult(ctx, r.finish(ctx, &state, "", StopStepLimit))
		}

		turnID := fmt.Sprintf("turn-%d", step+1)
		turn, stopReason, err := r.streamModel(ctx, &state, turnID, TurnRequest{
			Instructions: r.instructions,
			Messages:     append([]Message(nil), state.session.Messages...),
			Tools:        cloneToolSpecs(r.toolSpecs),
			Session:      state.session,
		})
		if err != nil {
			if stopReason == "" {
				stopReason = StopModelError
			}
			if turn.accepted {
				if turn.assembled.StopReason != "" {
					stopReason = turn.assembled.StopReason
				}
				result, saveErr := r.saveResult(ctx, state.result(stopReason))
				if saveErr != nil {
					return result, saveErr
				}
				return result, err
			}
			result, saveErr := r.saveResult(ctx, state.fail(stopReason, eventPayload{turnID: turnID, err: err}))
			if saveErr != nil {
				return result, saveErr
			}
			return result, err
		}

		state.session.Messages = append(state.session.Messages, turn.assembled.Messages...)
		state.text += turn.assembled.Text
		if !turn.assembled.Usage.empty() {
			state.usage = turn.assembled.Usage
		}

		if len(turn.assembled.ToolCalls) == 0 {
			stop := turn.assembled.StopReason
			if stop != "" {
				return r.saveResult(ctx, state.result(stop))
			}
			if stop == "" {
				stop = StopComplete
			}
			return r.saveResult(ctx, r.finish(ctx, &state, turnID, stop))
		}

		for _, call := range turn.assembled.ToolCalls {
			if err := r.callTool(ctx, &state, turnID, call); err != nil {
				return r.saveResult(ctx, *err)
			}
		}
	}
}

type modelTurnStream struct {
	assembled AssembledRun
	accepted  bool
}

func (r *runner) streamModel(ctx context.Context, state *runState, turnID string, request TurnRequest) (modelTurnStream, StopReason, error) {
	operation := retryOperation[modelTurnStream]{
		state:        state,
		turnID:       turnID,
		target:       RetryTarget{Kind: RetryTargetModel, TurnID: turnID},
		reason:       RetryReasonModelError,
		disabledStop: StopModelError,
		retryable:    true,
		call: func() (modelTurnStream, error) {
			return r.callModelStream(ctx, state, turnID, request)
		},
	}
	if r.retry.MaxAttempts <= 1 {
		turn, err := operation.call()
		return turn, operation.disabledStop, err
	}
	return r.retryModel(ctx, operation)
}

func (r *runner) callModelStream(ctx context.Context, state *runState, turnID string, request TurnRequest) (modelTurnStream, error) {
	var events []Event
	err := r.model.Stream(ctx, request, func(event Event) {
		event.TurnID = defaultString(event.TurnID, turnID)
		state.modelEvent(event)
		events = append(events, event)
	})
	if len(events) == 0 {
		return modelTurnStream{}, err
	}
	assembled, assembleErr := assembleTurnEvents(events)
	if assembleErr != nil {
		return modelTurnStream{assembled: assembled, accepted: true}, assembleErr
	}
	if err != nil {
		if assembled.Err == nil {
			return modelTurnStream{assembled: assembled, accepted: true}, StreamDivergenceError{EventIndex: len(events) - 1, Reason: "accepted stream error missing terminal error event"}
		}
		if !sameStreamError(err, assembled.Err) {
			return modelTurnStream{assembled: assembled, accepted: true}, StreamDivergenceError{EventIndex: terminalErrorIndex(events), Reason: "stream error does not match terminal error event"}
		}
	}
	return modelTurnStream{assembled: assembled, accepted: true}, err
}

func (r *runner) retryModel(ctx context.Context, operation retryOperation[modelTurnStream]) (modelTurnStream, StopReason, error) {
	maxAttempts := r.retry.MaxAttempts
	delay := r.retry.Delay
	for attempt := 1; ; attempt++ {
		value, err := operation.call()
		if err == nil {
			if attempt > 1 {
				operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeSucceeded})
			}
			return value, "", nil
		}
		if value.accepted {
			return value, StopModelError, err
		}

		operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeFailed})
		nextAttempt := attempt + 1
		if nextAttempt > maxAttempts {
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeExhausted, StopReason: StopRetryExhausted})
			return modelTurnStream{}, StopRetryExhausted, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeCanceled, StopReason: StopCanceled})
			return modelTurnStream{}, StopCanceled, ctxErr
		}
		operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeConsidered, Delay: delay})
		decision, policyErr := r.decide(ctx, operation.state, operation.turnID, Decision{Kind: DecisionRetry, Retry: operation.context(nextAttempt, maxAttempts, err), Request: operation.state.request, Session: operation.state.session})
		if policyErr != nil {
			return modelTurnStream{}, StopPolicyDenied, policyErr
		}
		if !decision.Allowed {
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeDenied, StopReason: StopPolicyDenied})
			return modelTurnStream{}, StopPolicyDenied, err
		}
		if decision.Retry.MaxAttempts > 0 && decision.Retry.MaxAttempts < maxAttempts {
			maxAttempts = decision.Retry.MaxAttempts
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeConstrained, Delay: delay})
			if nextAttempt > maxAttempts {
				operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeExhausted, StopReason: StopRetryExhausted})
				return modelTurnStream{}, StopRetryExhausted, err
			}
		}
		if decision.Retry.Delay > delay {
			delay = decision.Retry.Delay
		}
		if err := sleep(ctx, delay); err != nil {
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeCanceled, Delay: delay, StopReason: StopCanceled})
			return modelTurnStream{}, StopCanceled, err
		}
		operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeAttempted, Delay: delay})
	}
}

func sleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func requestSession(request RunRequest) Session {
	session := cloneSession(request.Session)
	sessionID := request.SessionID
	if sessionID == "" {
		sessionID = session.ID
	}
	if sessionID != "" {
		session.ID = sessionID
	}
	return session
}

func (r *runner) loadSession(ctx context.Context, state *runState, request RunRequest) (Session, StopReason, error) {
	session := requestSession(request)
	if r.sessionStore == nil || session.ID == "" {
		return session, "", nil
	}
	load := func() (Session, error) {
		stored, err := r.sessionStore.LoadSession(ctx, session.ID)
		if err != nil {
			return Session{}, err
		}
		if len(stored.Messages) == 0 && stored.Values == nil {
			return session, nil
		}
		stored.ID = session.ID
		return cloneSession(stored), nil
	}
	if r.retry.MaxAttempts <= 1 {
		loaded, err := load()
		return loaded, "", err
	}
	return r.retryRuntime(ctx, state, RetryTarget{Kind: RetryTargetRuntime}, RetryReasonRuntimeError, load)
}

func (r *runner) retryRuntime(ctx context.Context, state *runState, target RetryTarget, reason RetryReason, operation func() (Session, error)) (Session, StopReason, error) {
	return runRetryOperation(ctx, r, retryOperation[Session]{
		state:        state,
		target:       target,
		reason:       reason,
		disabledStop: "",
		retryable:    true,
		call:         operation,
	})
}

func (r *runner) saveResult(ctx context.Context, result RunResult) (RunResult, error) {
	if r.sessionStore == nil || result.Session.ID == "" {
		return result, nil
	}
	if err := r.sessionStore.SaveSession(ctx, result.Session); err != nil {
		return result, err
	}
	return result, nil
}

func (r *runner) callTool(ctx context.Context, state *runState, turnID string, call ToolCall) *RunResult {
	registered, ok := r.tools[call.Name]
	spec := ToolSpec{}
	if ok {
		spec = cloneToolSpec(registered.spec)
	}
	state.toolCall(turnID, spec, call)
	if !ok {
		result := state.fail(StopToolError, eventPayload{turnID: turnID, toolCallID: call.ID, err: fmt.Errorf("goagent: unknown tool %q", call.Name)})
		return &result
	}

	policyDecision, err := r.decide(ctx, state, turnID, Decision{Kind: DecisionToolCall, ToolCall: call, Tool: spec, Session: state.session})
	if err != nil {
		result := state.fail(StopPolicyDenied, eventPayload{turnID: turnID, toolCallID: call.ID, tool: spec, err: err})
		return &result
	}
	if !policyDecision.Allowed {
		returnResult := r.finish(ctx, state, turnID, StopPolicyDenied)
		return &returnResult
	}
	if policyDecision.ToolCall != nil {
		constrained := *policyDecision.ToolCall
		if constrained.Name == "" {
			constrained.Name = call.Name
		}
		if constrained.Name != call.Name {
			result := state.fail(StopPolicyDenied, eventPayload{turnID: turnID, toolCallID: call.ID, tool: spec, err: fmt.Errorf("goagent: policy cannot change tool %q to %q", call.Name, constrained.Name)})
			return &result
		}
		constrained.ID = call.ID
		call = constrained
	}

	toolResult, stopReason, err := registered.callWithRetry(ctx, r, state, turnID, call)
	if err != nil {
		if stopReason == "" {
			stopReason = StopToolError
		}
		result := state.fail(stopReason, eventPayload{turnID: turnID, toolCallID: call.ID, tool: spec, toolCall: call, err: err})
		return &result
	}
	message := toolResultMessage(call, toolResult)
	resultDecision, err := r.decide(ctx, state, turnID, Decision{Kind: DecisionToolResult, ToolCall: call, Tool: spec, ToolResult: toolResult, Message: message, Session: state.session})
	if err != nil {
		result := state.fail(StopPolicyDenied, eventPayload{turnID: turnID, toolCallID: call.ID, tool: spec, err: err})
		return &result
	}
	if !resultDecision.Allowed {
		returnResult := r.finish(ctx, state, turnID, StopPolicyDenied)
		return &returnResult
	}
	state.session.Messages = append(state.session.Messages, message)
	state.toolResult(turnID, spec, call, toolResult, message)
	return nil
}

func toolResultMessage(call ToolCall, result ToolResult) Message {
	if result.CallID == "" {
		result.CallID = call.ID
	}
	if result.Name == "" {
		result.Name = call.Name
	}
	return Message{
		Role:       RoleTool,
		Content:    result.Content,
		Name:       result.Name,
		ToolCallID: result.CallID,
		Blocks:     []Block{{ID: "tool-result-" + result.CallID, Kind: BlockToolResult, ToolResult: result}},
	}
}

func textMessage(role Role, text string) Message {
	return Message{Role: role, Content: text, Blocks: []Block{{Kind: BlockText, Text: text}}}
}

type retryOperation[T any] struct {
	state        *runState
	turnID       string
	toolCallID   string
	tool         ToolSpec
	toolCall     ToolCall
	target       RetryTarget
	reason       RetryReason
	disabledStop StopReason
	retryable    bool
	blockReason  RetryReason
	blockStop    StopReason
	call         func() (T, error)
}

func runRetryOperation[T any](ctx context.Context, r *runner, operation retryOperation[T]) (T, StopReason, error) {
	maxAttempts := r.retry.MaxAttempts
	if maxAttempts <= 1 {
		value, err := operation.call()
		return value, operation.disabledStop, err
	}

	delay := r.retry.Delay
	for attempt := 1; ; attempt++ {
		value, err := operation.call()
		if err == nil {
			if attempt > 1 {
				operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeSucceeded})
			}
			return value, "", nil
		}

		operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeFailed})
		nextAttempt := attempt + 1
		if nextAttempt > maxAttempts {
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeExhausted, StopReason: StopRetryExhausted})
			var zero T
			return zero, StopRetryExhausted, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeCanceled, StopReason: StopCanceled})
			var zero T
			return zero, StopCanceled, ctxErr
		}
		if !operation.retryable {
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.blockReason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeBlocked, StopReason: operation.blockStop})
			var zero T
			return zero, operation.blockStop, err
		}

		operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeConsidered, Delay: delay})
		decision, policyErr := r.decide(ctx, operation.state, operation.turnID, Decision{Kind: DecisionRetry, Retry: operation.context(nextAttempt, maxAttempts, err), Request: operation.state.request, ToolCall: operation.toolCall, Tool: operation.tool, Session: operation.state.session})
		if policyErr != nil {
			var zero T
			return zero, StopPolicyDenied, policyErr
		}
		if !decision.Allowed {
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeDenied, StopReason: StopPolicyDenied})
			var zero T
			return zero, StopPolicyDenied, err
		}
		if decision.Retry.MaxAttempts > 0 && decision.Retry.MaxAttempts < maxAttempts {
			maxAttempts = decision.Retry.MaxAttempts
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeConstrained, Delay: delay})
			if nextAttempt > maxAttempts {
				operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeExhausted, StopReason: StopRetryExhausted})
				var zero T
				return zero, StopRetryExhausted, err
			}
		}
		if decision.Retry.Delay > delay {
			delay = decision.Retry.Delay
		}
		if err := sleep(ctx, delay); err != nil {
			operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeCanceled, Delay: delay, StopReason: StopCanceled})
			var zero T
			return zero, StopCanceled, err
		}
		operation.sendRetry(RetryEvent{Target: operation.target, Reason: operation.reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeAttempted, Delay: delay})
	}
}

func (operation retryOperation[T]) context(attempt int, maxAttempts int, err error) RetryContext {
	return RetryContext{
		Target:      operation.target,
		Reason:      operation.reason,
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
		Request:     operation.state.request,
		Session:     operation.state.session,
		TurnID:      operation.turnID,
		ToolCall:    operation.toolCall,
		Tool:        operation.tool,
		Err:         err,
	}
}

func (operation retryOperation[T]) sendRetry(retry RetryEvent) {
	operation.state.retry(operation.turnID, operation.toolCallID, operation.tool, operation.toolCall, retry)
}

func (r *runner) decide(ctx context.Context, state *runState, turnID string, decision Decision) (PolicyDecision, error) {
	policyDecision, err := r.policy.Decide(ctx, cloneDecision(decision))
	if r.policyExplicit || decision.Kind == DecisionToolCall || decision.Kind == DecisionRetry {
		state.policyDecision(turnID, decision, policyDecision)
	}
	return policyDecision, err
}

func (r *runner) finish(ctx context.Context, state *runState, turnID string, reason StopReason) RunResult {
	_, err := r.decide(ctx, state, turnID, Decision{Kind: DecisionStop, Request: state.request, Session: state.session, StopReason: reason, Events: append([]Event(nil), state.events...)})
	if err != nil {
		return state.fail(StopPolicyDenied, eventPayload{turnID: turnID, err: err})
	}
	return state.finish(reason)
}

type runState struct {
	runID   string
	ctx     context.Context
	request RunRequest
	session Session
	text    string
	usage   Usage
	seq     int64
	emit    func(Event)
	sinks   []EventSink
	events  []Event
}

type eventPayload struct {
	turnID         string
	messageID      string
	blockID        string
	blockKind      BlockKind
	toolCallID     string
	text           string
	message        Message
	tool           ToolSpec
	toolCall       ToolCall
	toolCallDelta  ToolCallDelta
	toolResult     ToolResult
	usage          Usage
	decision       Decision
	policyDecision PolicyDecision
	retry          RetryEvent
	stopReason     StopReason
	err            error
}

func (s *runState) toolCall(turnID string, tool ToolSpec, call ToolCall) {
	s.send(EventToolCall, eventPayload{turnID: turnID, toolCallID: call.ID, tool: tool, toolCall: call})
}

func (s *runState) toolResult(turnID string, tool ToolSpec, call ToolCall, result ToolResult, message Message) {
	blockID := ""
	if len(message.Blocks) > 0 {
		blockID = message.Blocks[0].ID
	}
	s.send(EventToolResult, eventPayload{turnID: turnID, blockID: blockID, blockKind: BlockToolResult, toolCallID: call.ID, tool: tool, toolResult: result, message: message})
}

func (s *runState) policyDecision(turnID string, decision Decision, policyDecision PolicyDecision) {
	s.send(EventPolicyDecision, eventPayload{turnID: turnID, toolCallID: decision.ToolCall.ID, tool: decision.Tool, decision: decision, policyDecision: policyDecision})
}

func (s *runState) retry(turnID string, toolCallID string, tool ToolSpec, call ToolCall, retry RetryEvent) {
	s.send(EventRetry, eventPayload{turnID: turnID, toolCallID: toolCallID, tool: tool, toolCall: call, retry: retry})
}

func (s *runState) send(kind EventKind, payload eventPayload) {
	event := Event{
		Kind:           kind,
		TurnID:         payload.turnID,
		MessageID:      payload.messageID,
		BlockID:        payload.blockID,
		BlockKind:      payload.blockKind,
		ToolCallID:     payload.toolCallID,
		Text:           payload.text,
		Message:        payload.message,
		Tool:           cloneToolSpec(payload.tool),
		ToolCall:       payload.toolCall,
		ToolCallDelta:  payload.toolCallDelta,
		ToolResult:     payload.toolResult,
		Usage:          payload.usage,
		Decision:       cloneDecision(payload.decision),
		PolicyDecision: payload.policyDecision,
		Retry:          payload.retry,
		StopReason:     payload.stopReason,
		Err:            payload.err,
	}
	s.seq++
	event.Sequence = s.seq
	event.RunID = s.runID
	s.events = append(s.events, event)
	s.emit(event)
	s.notifySinks(event)
}

func (s *runState) modelEvent(event Event) {
	s.send(event.Kind, eventPayload{
		turnID:        event.TurnID,
		messageID:     event.MessageID,
		blockID:       event.BlockID,
		blockKind:     event.BlockKind,
		toolCallID:    event.ToolCallID,
		text:          event.Text,
		message:       event.Message,
		tool:          event.Tool,
		toolCall:      event.ToolCall,
		toolCallDelta: event.ToolCallDelta,
		toolResult:    event.ToolResult,
		usage:         event.Usage,
		stopReason:    event.StopReason,
		err:           event.Err,
	})
}

func (s *runState) notifySinks(event Event) {
	for _, sink := range s.sinks {
		func() {
			defer func() { _ = recover() }()
			sink.HandleEvent(s.ctx, event)
		}()
	}
}

func (s *runState) fail(reason StopReason, payload eventPayload) RunResult {
	s.send(EventError, payload)
	return s.finish(reason)
}

func (s *runState) finish(reason StopReason) RunResult {
	s.send(EventStop, eventPayload{stopReason: reason})
	return s.result(reason)
}

func (s *runState) result(reason StopReason) RunResult {
	return RunResult{Text: s.text, StopReason: reason, Usage: s.usage, Session: s.session, Events: cloneEvents(s.events)}
}

func defaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

type allowAllPolicy struct{}

func (allowAllPolicy) Decide(context.Context, Decision) (PolicyDecision, error) {
	return PolicyDecision{Allowed: true}, nil
}

func cloneSession(session Session) Session {
	clone := session
	clone.Messages = cloneMessages(session.Messages)
	if session.Values != nil {
		clone.Values = make(map[string]any, len(session.Values))
		for key, value := range session.Values {
			clone.Values[key] = value
		}
	}
	return clone
}

func cloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	clone := make([]Message, len(messages))
	for i, message := range messages {
		clone[i] = cloneMessage(message)
	}
	return clone
}

func cloneMessage(message Message) Message {
	message.Blocks = cloneBlocks(message.Blocks)
	message.ToolCalls = append([]ToolCall(nil), message.ToolCalls...)
	return message
}

func cloneBlocks(blocks []Block) []Block {
	if blocks == nil {
		return nil
	}
	clone := make([]Block, len(blocks))
	for i, block := range blocks {
		block.ToolResult = cloneToolResult(block.ToolResult)
		clone[i] = block
	}
	return clone
}

func cloneToolResult(result ToolResult) ToolResult {
	result.JSON = cloneJSONValue(result.JSON)
	return result
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		clone := make(map[string]any, len(typed))
		for key, value := range typed {
			clone[key] = cloneJSONValue(value)
		}
		return clone
	case []any:
		clone := make([]any, len(typed))
		for i, value := range typed {
			clone[i] = cloneJSONValue(value)
		}
		return clone
	default:
		return value
	}
}

func cloneEvents(events []Event) []Event {
	if events == nil {
		return nil
	}
	clone := make([]Event, len(events))
	for i, event := range events {
		event.Message = cloneMessage(event.Message)
		event.Tool = cloneToolSpec(event.Tool)
		event.ToolResult = cloneToolResult(event.ToolResult)
		event.Decision = cloneDecision(event.Decision)
		clone[i] = event
	}
	return clone
}

type registeredTool struct {
	tool Tool
	spec ToolSpec
}

func buildToolRegistry(agentTools []Tool) (map[string]registeredTool, []ToolSpec, error) {
	tools := make(map[string]registeredTool, len(agentTools))
	specs := make([]ToolSpec, 0, len(agentTools))
	for _, tool := range agentTools {
		if tool == nil {
			return nil, nil, fmt.Errorf("goagent: agent tool cannot be nil")
		}
		spec := toolSpec(tool)
		if err := validateToolName(spec.Name); err != nil {
			return nil, nil, err
		}
		if _, exists := tools[spec.Name]; exists {
			return nil, nil, fmt.Errorf("goagent: duplicate tool %q", spec.Name)
		}
		tools[spec.Name] = registeredTool{tool: tool, spec: cloneToolSpec(spec)}
		specs = append(specs, cloneToolSpec(spec))
	}
	return tools, specs, nil
}

func toolSpec(tool Tool) ToolSpec {
	spec := ToolSpec{Name: tool.Name(), Description: tool.Description(), Schema: tool.Schema()}
	if provider, ok := tool.(toolMetadataProvider); ok {
		metadata := provider.Metadata()
		spec.Safety = metadata.Safety
		spec.Constraints = metadata.Constraints
	}
	return cloneToolSpec(spec)
}

func cloneToolSpecs(specs []ToolSpec) []ToolSpec {
	clone := make([]ToolSpec, len(specs))
	for i, spec := range specs {
		clone[i] = cloneToolSpec(spec)
	}
	return clone
}

func cloneToolSpec(spec ToolSpec) ToolSpec {
	spec.Schema = cloneToolSchema(spec.Schema)
	return spec
}

func cloneDecision(decision Decision) Decision {
	decision.Tool = cloneToolSpec(decision.Tool)
	decision.ToolResult = cloneToolResult(decision.ToolResult)
	decision.Message = cloneMessage(decision.Message)
	decision.Retry.Tool = cloneToolSpec(decision.Retry.Tool)
	decision.Session = cloneSession(decision.Session)
	decision.Events = cloneEvents(decision.Events)
	return decision
}
