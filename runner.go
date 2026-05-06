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
		state.session.Messages = append(state.session.Messages, Message{Role: RoleUser, Content: request.Input})
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
		return r.saveResult(ctx, state.finish(StopPolicyDenied))
	}
	if runDecision.MaxSteps > 0 && runDecision.MaxSteps < maxSteps {
		maxSteps = runDecision.MaxSteps
	}

	for step := 0; ; step++ {
		if err := ctx.Err(); err != nil {
			return r.saveResult(ctx, state.fail(StopCanceled, eventPayload{err: err}))
		}
		if step >= maxSteps {
			return r.saveResult(ctx, state.finish(StopStepLimit))
		}

		turnID := fmt.Sprintf("turn-%d", step+1)
		turn, stopReason, err := r.turnModel(ctx, &state, turnID, TurnRequest{
			Instructions: r.instructions,
			Messages:     append([]Message(nil), state.session.Messages...),
			Tools:        cloneToolSpecs(r.toolSpecs),
			Session:      state.session,
		})
		if err != nil {
			if stopReason == "" {
				stopReason = StopModelError
			}
			return r.saveResult(ctx, state.fail(stopReason, eventPayload{turnID: turnID, err: err}))
		}

		if turn.Message.Content != "" || turn.Message.Role != "" || len(turn.ToolCalls) > 0 {
			message := turn.Message
			if message.Role == "" {
				message.Role = RoleAssistant
			}
			if len(message.ToolCalls) == 0 {
				message.ToolCalls = append([]ToolCall(nil), turn.ToolCalls...)
			}
			state.session.Messages = append(state.session.Messages, message)
			if message.Content != "" {
				state.text += message.Content
				state.textDelta(turnID, message)
			}
		}

		if len(turn.ToolCalls) == 0 {
			if turn.StopReason == "" {
				turn.StopReason = StopComplete
			}
			return r.saveResult(ctx, state.finish(turn.StopReason))
		}

		for _, call := range turn.ToolCalls {
			if err := r.callTool(ctx, &state, turnID, call); err != nil {
				return r.saveResult(ctx, *err)
			}
		}
	}
}

func (r *runner) turnModel(ctx context.Context, state *runState, turnID string, request TurnRequest) (TurnResult, StopReason, error) {
	return runRetryOperation(ctx, r, retryOperation[TurnResult]{
		state:        state,
		turnID:       turnID,
		target:       RetryTarget{Kind: RetryTargetModel, TurnID: turnID},
		reason:       RetryReasonModelError,
		disabledStop: StopModelError,
		retryable:    true,
		call: func() (TurnResult, error) {
			return r.model.Turn(ctx, request)
		},
	})
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
		returnResult := state.finish(StopPolicyDenied)
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
	resultDecision, err := r.decide(ctx, state, turnID, Decision{Kind: DecisionToolResult, ToolCall: call, Tool: spec, ToolResult: toolResult, Session: state.session})
	if err != nil {
		result := state.fail(StopPolicyDenied, eventPayload{turnID: turnID, toolCallID: call.ID, tool: spec, err: err})
		return &result
	}
	if !resultDecision.Allowed {
		returnResult := state.finish(StopPolicyDenied)
		return &returnResult
	}
	state.session.Messages = append(state.session.Messages, Message{
		Role:       RoleTool,
		Content:    toolResult.Content,
		Name:       toolResult.Name,
		ToolCallID: toolResult.CallID,
	})
	state.toolResult(turnID, spec, call, toolResult)
	return nil
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

type runState struct {
	runID   string
	ctx     context.Context
	request RunRequest
	session Session
	text    string
	seq     int64
	emit    func(Event)
	sinks   []EventSink
	events  []Event
}

type eventPayload struct {
	turnID         string
	toolCallID     string
	text           string
	message        Message
	tool           ToolSpec
	toolCall       ToolCall
	toolResult     ToolResult
	decision       Decision
	policyDecision PolicyDecision
	retry          RetryEvent
	stopReason     StopReason
	err            error
}

func (s *runState) textDelta(turnID string, message Message) {
	s.send(EventTextDelta, eventPayload{turnID: turnID, text: message.Content, message: message})
}

func (s *runState) toolCall(turnID string, tool ToolSpec, call ToolCall) {
	s.send(EventToolCall, eventPayload{turnID: turnID, toolCallID: call.ID, tool: tool, toolCall: call})
}

func (s *runState) toolResult(turnID string, tool ToolSpec, call ToolCall, result ToolResult) {
	s.send(EventToolResult, eventPayload{turnID: turnID, toolCallID: call.ID, tool: tool, toolResult: result})
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
		ToolCallID:     payload.toolCallID,
		Text:           payload.text,
		Message:        payload.message,
		Tool:           cloneToolSpec(payload.tool),
		ToolCall:       payload.toolCall,
		ToolResult:     payload.toolResult,
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
	return RunResult{Text: s.text, StopReason: reason, Session: s.session, Events: append([]Event(nil), s.events...)}
}

type allowAllPolicy struct{}

func (allowAllPolicy) Decide(context.Context, Decision) (PolicyDecision, error) {
	return PolicyDecision{Allowed: true}, nil
}

func cloneSession(session Session) Session {
	clone := session
	clone.Messages = append([]Message(nil), session.Messages...)
	if session.Values != nil {
		clone.Values = make(map[string]any, len(session.Values))
		for key, value := range session.Values {
			clone.Values[key] = value
		}
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
	decision.Retry.Tool = cloneToolSpec(decision.Retry.Tool)
	return decision
}
