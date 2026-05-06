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

	tools := make(map[string]Tool, len(agent.Tools))
	specs := make([]ToolSpec, 0, len(agent.Tools))
	for _, tool := range agent.Tools {
		if tool == nil {
			return nil, fmt.Errorf("goagent: agent tool cannot be nil")
		}
		name := tool.Name()
		if err := validateToolName(name); err != nil {
			return nil, err
		}
		if _, exists := tools[name]; exists {
			return nil, fmt.Errorf("goagent: duplicate tool %q", name)
		}
		tools[name] = tool
		specs = append(specs, toolSpec(tool))
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
	tools          map[string]Tool
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
		return state.fail(stopReason, Event{Err: err}), err
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
		return r.saveResult(ctx, state.fail(StopPolicyDenied, Event{Err: err}))
	}
	if !runDecision.Allowed {
		return r.saveResult(ctx, state.finish(StopPolicyDenied))
	}
	if runDecision.MaxSteps > 0 && runDecision.MaxSteps < maxSteps {
		maxSteps = runDecision.MaxSteps
	}

	for step := 0; ; step++ {
		if err := ctx.Err(); err != nil {
			return r.saveResult(ctx, state.fail(StopCanceled, Event{Err: err}))
		}
		if step >= maxSteps {
			return r.saveResult(ctx, state.finish(StopStepLimit))
		}

		turnID := fmt.Sprintf("turn-%d", step+1)
		turn, stopReason, err := r.turnModel(ctx, &state, turnID, TurnRequest{
			Instructions: r.instructions,
			Messages:     append([]Message(nil), state.session.Messages...),
			Tools:        append([]ToolSpec(nil), r.toolSpecs...),
			Session:      state.session,
		})
		if err != nil {
			if stopReason == "" {
				stopReason = StopModelError
			}
			return r.saveResult(ctx, state.fail(stopReason, Event{TurnID: turnID, Err: err}))
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
				state.send(Event{Kind: EventTextDelta, TurnID: turnID, Text: message.Content, Message: message})
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
	maxAttempts := r.retry.MaxAttempts
	if maxAttempts <= 1 {
		turn, err := r.model.Turn(ctx, request)
		return turn, StopModelError, err
	}
	delay := r.retry.Delay
	for attempt := 1; ; attempt++ {
		turn, err := r.model.Turn(ctx, request)
		if err == nil {
			if attempt > 1 {
				state.send(Event{Kind: EventRetry, TurnID: turnID, Retry: RetryEvent{Target: RetryTarget{Kind: RetryTargetModel, TurnID: turnID}, Reason: RetryReasonModelError, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeSucceeded}})
			}
			return turn, "", nil
		}

		target := RetryTarget{Kind: RetryTargetModel, TurnID: turnID}
		state.send(Event{Kind: EventRetry, TurnID: turnID, Retry: RetryEvent{Target: target, Reason: RetryReasonModelError, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeFailed}})
		nextAttempt := attempt + 1
		if nextAttempt > maxAttempts {
			state.send(Event{Kind: EventRetry, TurnID: turnID, Retry: RetryEvent{Target: target, Reason: RetryReasonModelError, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeExhausted, StopReason: StopRetryExhausted}})
			return TurnResult{}, StopRetryExhausted, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			state.send(Event{Kind: EventRetry, TurnID: turnID, Retry: RetryEvent{Target: target, Reason: RetryReasonModelError, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeCanceled, StopReason: StopCanceled}})
			return TurnResult{}, StopCanceled, ctxErr
		}

		retryContext := RetryContext{Target: target, Reason: RetryReasonModelError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Request: state.request, Session: state.session, TurnID: turnID, Err: err}
		state.send(Event{Kind: EventRetry, TurnID: turnID, Retry: RetryEvent{Target: target, Reason: RetryReasonModelError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeConsidered, Delay: delay}})
		decision, policyErr := r.decide(ctx, state, turnID, Decision{Kind: DecisionRetry, Retry: retryContext, Request: state.request, Session: state.session})
		if policyErr != nil {
			return TurnResult{}, StopPolicyDenied, policyErr
		}
		if !decision.Allowed {
			state.send(Event{Kind: EventRetry, TurnID: turnID, Retry: RetryEvent{Target: target, Reason: RetryReasonModelError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeDenied, StopReason: StopPolicyDenied}})
			return TurnResult{}, StopPolicyDenied, err
		}
		if decision.Retry.MaxAttempts > 0 && decision.Retry.MaxAttempts < maxAttempts {
			maxAttempts = decision.Retry.MaxAttempts
			state.send(Event{Kind: EventRetry, TurnID: turnID, Retry: RetryEvent{Target: target, Reason: RetryReasonModelError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeConstrained, Delay: delay}})
			if nextAttempt > maxAttempts {
				state.send(Event{Kind: EventRetry, TurnID: turnID, Retry: RetryEvent{Target: target, Reason: RetryReasonModelError, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeExhausted, StopReason: StopRetryExhausted}})
				return TurnResult{}, StopRetryExhausted, err
			}
		}
		if decision.Retry.Delay > delay {
			delay = decision.Retry.Delay
		}
		if err := sleep(ctx, delay); err != nil {
			state.send(Event{Kind: EventRetry, TurnID: turnID, Retry: RetryEvent{Target: target, Reason: RetryReasonModelError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeCanceled, Delay: delay, StopReason: StopCanceled}})
			return TurnResult{}, StopCanceled, err
		}
		state.send(Event{Kind: EventRetry, TurnID: turnID, Retry: RetryEvent{Target: target, Reason: RetryReasonModelError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeAttempted, Delay: delay}})
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
	maxAttempts := r.retry.MaxAttempts
	delay := r.retry.Delay
	for attempt := 1; ; attempt++ {
		session, err := operation()
		if err == nil {
			if attempt > 1 {
				state.send(Event{Kind: EventRetry, Retry: RetryEvent{Target: target, Reason: reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeSucceeded}})
			}
			return session, "", nil
		}
		state.send(Event{Kind: EventRetry, Retry: RetryEvent{Target: target, Reason: reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeFailed}})
		nextAttempt := attempt + 1
		if nextAttempt > maxAttempts {
			state.send(Event{Kind: EventRetry, Retry: RetryEvent{Target: target, Reason: reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeExhausted, StopReason: StopRetryExhausted}})
			return Session{}, StopRetryExhausted, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			state.send(Event{Kind: EventRetry, Retry: RetryEvent{Target: target, Reason: reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeCanceled, StopReason: StopCanceled}})
			return Session{}, StopCanceled, ctxErr
		}
		state.send(Event{Kind: EventRetry, Retry: RetryEvent{Target: target, Reason: reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeConsidered, Delay: delay}})
		decision, policyErr := r.decide(ctx, state, "", Decision{Kind: DecisionRetry, Retry: RetryContext{Target: target, Reason: reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Request: state.request, Session: state.session, Err: err}, Request: state.request, Session: state.session})
		if policyErr != nil {
			return Session{}, StopPolicyDenied, policyErr
		}
		if !decision.Allowed {
			state.send(Event{Kind: EventRetry, Retry: RetryEvent{Target: target, Reason: reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeDenied, StopReason: StopPolicyDenied}})
			return Session{}, StopPolicyDenied, err
		}
		if decision.Retry.MaxAttempts > 0 && decision.Retry.MaxAttempts < maxAttempts {
			maxAttempts = decision.Retry.MaxAttempts
			state.send(Event{Kind: EventRetry, Retry: RetryEvent{Target: target, Reason: reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeConstrained, Delay: delay}})
			if nextAttempt > maxAttempts {
				state.send(Event{Kind: EventRetry, Retry: RetryEvent{Target: target, Reason: reason, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeExhausted, StopReason: StopRetryExhausted}})
				return Session{}, StopRetryExhausted, err
			}
		}
		if decision.Retry.Delay > delay {
			delay = decision.Retry.Delay
		}
		if err := sleep(ctx, delay); err != nil {
			state.send(Event{Kind: EventRetry, Retry: RetryEvent{Target: target, Reason: reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeCanceled, Delay: delay, StopReason: StopCanceled}})
			return Session{}, StopCanceled, err
		}
		state.send(Event{Kind: EventRetry, Retry: RetryEvent{Target: target, Reason: reason, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeAttempted, Delay: delay}})
	}
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
	tool, ok := r.tools[call.Name]
	spec := ToolSpec{}
	if ok {
		spec = toolSpec(tool)
	}
	state.send(Event{Kind: EventToolCall, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call})
	if !ok {
		result := state.fail(StopToolError, Event{TurnID: turnID, ToolCallID: call.ID, Err: fmt.Errorf("goagent: unknown tool %q", call.Name)})
		return &result
	}

	policyDecision, err := r.decide(ctx, state, turnID, Decision{Kind: DecisionToolCall, ToolCall: call, Tool: spec, Session: state.session})
	if err != nil {
		result := state.fail(StopPolicyDenied, Event{TurnID: turnID, ToolCallID: call.ID, Tool: spec, Err: err})
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
			result := state.fail(StopPolicyDenied, Event{TurnID: turnID, ToolCallID: call.ID, Tool: spec, Err: fmt.Errorf("goagent: policy cannot change tool %q to %q", call.Name, constrained.Name)})
			return &result
		}
		constrained.ID = call.ID
		call = constrained
	}

	toolResult, stopReason, err := r.callToolWithRetry(ctx, state, turnID, spec, tool, call)
	if err != nil {
		if stopReason == "" {
			stopReason = StopToolError
		}
		result := state.fail(stopReason, Event{TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Err: err})
		return &result
	}
	resultDecision, err := r.decide(ctx, state, turnID, Decision{Kind: DecisionToolResult, ToolCall: call, Tool: spec, ToolResult: toolResult, Session: state.session})
	if err != nil {
		result := state.fail(StopPolicyDenied, Event{TurnID: turnID, ToolCallID: call.ID, Tool: spec, Err: err})
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
	state.send(Event{Kind: EventToolResult, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolResult: toolResult})
	return nil
}

func (r *runner) callToolWithRetry(ctx context.Context, state *runState, turnID string, spec ToolSpec, tool Tool, call ToolCall) (ToolResult, StopReason, error) {
	maxAttempts := r.retry.MaxAttempts
	if maxAttempts <= 1 {
		result, err := tool.Call(ctx, call)
		return result, StopToolError, err
	}

	target := RetryTarget{Kind: RetryTargetTool, TurnID: turnID, ToolCallID: call.ID, ToolName: call.Name}
	delay := r.retry.Delay
	for attempt := 1; ; attempt++ {
		result, err := tool.Call(ctx, call)
		if err == nil {
			if attempt > 1 {
				state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolError, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeSucceeded}})
			}
			return result, "", nil
		}

		state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolError, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeFailed}})
		nextAttempt := attempt + 1
		if nextAttempt > maxAttempts {
			state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolError, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeExhausted, StopReason: StopRetryExhausted}})
			return ToolResult{}, StopRetryExhausted, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolError, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeCanceled, StopReason: StopCanceled}})
			return ToolResult{}, StopCanceled, ctxErr
		}
		if !spec.Safety.Retryable {
			state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolRetryBlocked, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeBlocked, StopReason: StopToolError}})
			return ToolResult{}, StopToolError, err
		}

		state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeConsidered, Delay: delay}})
		decision, policyErr := r.decide(ctx, state, turnID, Decision{Kind: DecisionRetry, Retry: RetryContext{Target: target, Reason: RetryReasonToolError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Request: state.request, Session: state.session, TurnID: turnID, ToolCall: call, Tool: spec, Err: err}, Request: state.request, ToolCall: call, Tool: spec, Session: state.session})
		if policyErr != nil {
			return ToolResult{}, StopPolicyDenied, policyErr
		}
		if !decision.Allowed {
			state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeDenied, StopReason: StopPolicyDenied}})
			return ToolResult{}, StopPolicyDenied, err
		}
		if decision.Retry.MaxAttempts > 0 && decision.Retry.MaxAttempts < maxAttempts {
			maxAttempts = decision.Retry.MaxAttempts
			state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeConstrained, Delay: delay}})
			if nextAttempt > maxAttempts {
				state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolError, Attempt: attempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeExhausted, StopReason: StopRetryExhausted}})
				return ToolResult{}, StopRetryExhausted, err
			}
		}
		if decision.Retry.Delay > delay {
			delay = decision.Retry.Delay
		}
		if err := sleep(ctx, delay); err != nil {
			state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeCanceled, Delay: delay, StopReason: StopCanceled}})
			return ToolResult{}, StopCanceled, err
		}
		state.send(Event{Kind: EventRetry, TurnID: turnID, ToolCallID: call.ID, Tool: spec, ToolCall: call, Retry: RetryEvent{Target: target, Reason: RetryReasonToolError, Attempt: nextAttempt, MaxAttempts: maxAttempts, Outcome: RetryOutcomeAttempted, Delay: delay}})
	}
}

func (r *runner) decide(ctx context.Context, state *runState, turnID string, decision Decision) (PolicyDecision, error) {
	policyDecision, err := r.policy.Decide(ctx, decision)
	if r.policyExplicit || decision.Kind == DecisionToolCall || decision.Kind == DecisionRetry {
		state.send(Event{Kind: EventPolicyDecision, TurnID: turnID, ToolCallID: decision.ToolCall.ID, Tool: decision.Tool, Decision: decision, PolicyDecision: policyDecision})
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

func (s *runState) send(event Event) {
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

func (s *runState) fail(reason StopReason, event Event) RunResult {
	event.Kind = EventError
	s.send(event)
	return s.finish(reason)
}

func (s *runState) finish(reason StopReason) RunResult {
	s.send(Event{Kind: EventStop, StopReason: reason})
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

func toolSpec(tool Tool) ToolSpec {
	spec := ToolSpec{Name: tool.Name(), Description: tool.Description(), Schema: tool.Schema()}
	if provider, ok := tool.(toolMetadataProvider); ok {
		metadata := provider.Metadata()
		spec.Safety = metadata.Safety
		spec.Constraints = metadata.Constraints
	}
	return spec
}
