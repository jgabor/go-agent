package goagent

import (
	"context"
	"fmt"
	"sync/atomic"
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
	session, err := r.sessionForRequest(ctx, request)
	if err != nil {
		return RunResult{}, err
	}

	state := runState{
		runID:   fmt.Sprintf("run-%d", nextRunID.Add(1)),
		ctx:     ctx,
		request: request,
		session: session,
		emit:    emit,
		sinks:   r.eventSinks,
	}
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
		turn, err := r.model.Turn(ctx, TurnRequest{
			Instructions: r.instructions,
			Messages:     append([]Message(nil), state.session.Messages...),
			Tools:        append([]ToolSpec(nil), r.toolSpecs...),
			Session:      state.session,
		})
		if err != nil {
			return r.saveResult(ctx, state.fail(StopModelError, Event{TurnID: turnID, Err: err}))
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

func (r *runner) sessionForRequest(ctx context.Context, request RunRequest) (Session, error) {
	session := cloneSession(request.Session)
	sessionID := request.SessionID
	if sessionID == "" {
		sessionID = session.ID
	}
	if sessionID != "" {
		session.ID = sessionID
	}

	if r.sessionStore == nil || sessionID == "" {
		return session, nil
	}

	stored, err := r.sessionStore.LoadSession(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	if len(stored.Messages) == 0 && stored.Values == nil {
		return session, nil
	}
	stored.ID = sessionID
	return cloneSession(stored), nil
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
	state.send(Event{Kind: EventToolCall, TurnID: turnID, ToolCallID: call.ID, ToolCall: call})

	tool, ok := r.tools[call.Name]
	if !ok {
		result := state.fail(StopToolError, Event{TurnID: turnID, ToolCallID: call.ID, Err: fmt.Errorf("goagent: unknown tool %q", call.Name)})
		return &result
	}

	policyDecision, err := r.decide(ctx, state, turnID, Decision{Kind: DecisionToolCall, ToolCall: call, Tool: toolSpec(tool), Session: state.session})
	if err != nil {
		result := state.fail(StopPolicyDenied, Event{TurnID: turnID, ToolCallID: call.ID, Err: err})
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
			result := state.fail(StopPolicyDenied, Event{TurnID: turnID, ToolCallID: call.ID, Err: fmt.Errorf("goagent: policy cannot change tool %q to %q", call.Name, constrained.Name)})
			return &result
		}
		constrained.ID = call.ID
		call = constrained
	}

	toolResult, err := tool.Call(ctx, call)
	if err != nil {
		result := state.fail(StopToolError, Event{TurnID: turnID, ToolCallID: call.ID, Err: err})
		return &result
	}
	resultDecision, err := r.decide(ctx, state, turnID, Decision{Kind: DecisionToolResult, ToolCall: call, Tool: toolSpec(tool), ToolResult: toolResult, Session: state.session})
	if err != nil {
		result := state.fail(StopPolicyDenied, Event{TurnID: turnID, ToolCallID: call.ID, Err: err})
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
	state.send(Event{Kind: EventToolResult, TurnID: turnID, ToolCallID: call.ID, ToolResult: toolResult})
	return nil
}

func (r *runner) decide(ctx context.Context, state *runState, turnID string, decision Decision) (PolicyDecision, error) {
	policyDecision, err := r.policy.Decide(ctx, decision)
	if r.policyExplicit || decision.Kind == DecisionToolCall {
		state.send(Event{Kind: EventPolicyDecision, TurnID: turnID, ToolCallID: decision.ToolCall.ID, Decision: decision, PolicyDecision: policyDecision})
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
	return ToolSpec{Name: tool.Name(), Description: tool.Description(), Schema: tool.Schema()}
}
