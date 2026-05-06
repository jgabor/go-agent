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

	policy := agent.Policy
	if policy == nil {
		policy = allowAllPolicy{}
	}

	return &runner{
		instructions: agent.Instructions,
		model:        agent.Model,
		tools:        tools,
		toolSpecs:    specs,
		policy:       policy,
	}, nil
}

type runner struct {
	instructions string
	model        Model
	tools        map[string]Tool
	toolSpecs    []ToolSpec
	policy       Policy
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
	state := runState{
		runID:   fmt.Sprintf("run-%d", nextRunID.Add(1)),
		request: request,
		session: cloneSession(request.Session),
		emit:    emit,
	}
	state.session.Messages = append([]Message(nil), request.Session.Messages...)
	if request.Input != "" {
		state.session.Messages = append(state.session.Messages, Message{Role: RoleUser, Content: request.Input})
	}

	maxSteps := request.MaxSteps
	if maxSteps == 0 {
		maxSteps = defaultMaxSteps
	}

	for step := 0; ; step++ {
		if err := ctx.Err(); err != nil {
			return state.fail(StopCanceled, Event{Err: err}), nil
		}
		if step >= maxSteps {
			return state.finish(StopStepLimit), nil
		}

		turnID := fmt.Sprintf("turn-%d", step+1)
		turn, err := r.model.Turn(ctx, TurnRequest{
			Instructions: r.instructions,
			Messages:     append([]Message(nil), state.session.Messages...),
			Tools:        append([]ToolSpec(nil), r.toolSpecs...),
			Session:      state.session,
		})
		if err != nil {
			return state.fail(StopModelError, Event{TurnID: turnID, Err: err}), nil
		}

		if turn.Message.Content != "" || turn.Message.Role != "" {
			message := turn.Message
			if message.Role == "" {
				message.Role = RoleAssistant
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
			return state.finish(turn.StopReason), nil
		}

		for _, call := range turn.ToolCalls {
			if err := r.callTool(ctx, &state, turnID, call); err != nil {
				return *err, nil
			}
		}
	}
}

func (r *runner) callTool(ctx context.Context, state *runState, turnID string, call ToolCall) *RunResult {
	state.send(Event{Kind: EventToolCall, TurnID: turnID, ToolCallID: call.ID, ToolCall: call})

	tool, ok := r.tools[call.Name]
	if !ok {
		result := state.fail(StopToolError, Event{TurnID: turnID, ToolCallID: call.ID, Err: fmt.Errorf("goagent: unknown tool %q", call.Name)})
		return &result
	}

	decision, err := r.policy.Decide(ctx, Decision{ToolCall: call, Tool: toolSpec(tool), Session: state.session})
	if err != nil {
		result := state.fail(StopPolicyDenied, Event{TurnID: turnID, ToolCallID: call.ID, Err: err})
		return &result
	}
	state.send(Event{Kind: EventPolicyDecision, TurnID: turnID, ToolCallID: call.ID})
	if !decision.Allowed {
		returnResult := state.finish(StopPolicyDenied)
		return &returnResult
	}

	toolResult, err := tool.Call(ctx, call)
	if err != nil {
		result := state.fail(StopToolError, Event{TurnID: turnID, ToolCallID: call.ID, Err: err})
		return &result
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

type runState struct {
	runID   string
	request RunRequest
	session Session
	text    string
	seq     int64
	emit    func(Event)
	events  []Event
}

func (s *runState) send(event Event) {
	s.seq++
	event.Sequence = s.seq
	event.RunID = s.runID
	s.events = append(s.events, event)
	s.emit(event)
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
