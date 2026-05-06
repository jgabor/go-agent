package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestRuntimeBehaviorSpecification(t *testing.T) {
	tests := []struct {
		name       string
		ctx        context.Context
		request    goagent.RunRequest
		runtime    *scriptedRuntime
		wantStop   goagent.StopReason
		wantEvents []goagent.EventKind
	}{
		{
			name:    "normal completion",
			ctx:     context.Background(),
			request: goagent.RunRequest{Input: "Should I bring a jacket?"},
			runtime: newScriptedRuntime(
				[]scriptedTurn{{result: goagent.TurnResult{
					Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Bring a jacket."},
					StopReason: goagent.StopComplete,
				}}},
				scriptedTool{},
				allowAllPolicy,
			),
			wantStop:   goagent.StopComplete,
			wantEvents: []goagent.EventKind{goagent.EventTextDelta, goagent.EventStop},
		},
		{
			name:    "tool call dispatch",
			ctx:     context.Background(),
			request: goagent.RunRequest{Input: "Weather in Austin?"},
			runtime: newScriptedRuntime(
				[]scriptedTurn{
					{result: goagent.TurnResult{ToolCalls: []goagent.ToolCall{weatherCall("call-1")}}},
					{result: goagent.TurnResult{
						Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Clear in Austin."},
						StopReason: goagent.StopComplete,
					}},
				},
				scriptedTool{},
				allowAllPolicy,
			),
			wantStop: goagent.StopComplete,
			wantEvents: []goagent.EventKind{
				goagent.EventToolCall,
				goagent.EventPolicyDecision,
				goagent.EventToolResult,
				goagent.EventTextDelta,
				goagent.EventStop,
			},
		},
		{
			name:    "tool error",
			ctx:     context.Background(),
			request: goagent.RunRequest{Input: "Weather in Austin?"},
			runtime: newScriptedRuntime(
				[]scriptedTurn{{result: goagent.TurnResult{ToolCalls: []goagent.ToolCall{weatherCall("call-1")}}}},
				scriptedTool{err: errors.New("tool failed")},
				allowAllPolicy,
			),
			wantStop: goagent.StopToolError,
			wantEvents: []goagent.EventKind{
				goagent.EventToolCall,
				goagent.EventPolicyDecision,
				goagent.EventError,
				goagent.EventStop,
			},
		},
		{
			name:    "model error",
			ctx:     context.Background(),
			request: goagent.RunRequest{Input: "Weather in Austin?"},
			runtime: newScriptedRuntime(
				[]scriptedTurn{{err: errors.New("model failed")}},
				scriptedTool{},
				allowAllPolicy,
			),
			wantStop:   goagent.StopModelError,
			wantEvents: []goagent.EventKind{goagent.EventError, goagent.EventStop},
		},
		{
			name:    "cancellation",
			ctx:     canceledContext(),
			request: goagent.RunRequest{Input: "Weather in Austin?"},
			runtime: newScriptedRuntime(
				[]scriptedTurn{{result: goagent.TurnResult{Message: goagent.Message{Content: "unreached"}}}},
				scriptedTool{},
				allowAllPolicy,
			),
			wantStop:   goagent.StopCanceled,
			wantEvents: []goagent.EventKind{goagent.EventError, goagent.EventStop},
		},
		{
			name:    "policy denial",
			ctx:     context.Background(),
			request: goagent.RunRequest{Input: "Weather in Austin?"},
			runtime: newScriptedRuntime(
				[]scriptedTurn{{result: goagent.TurnResult{ToolCalls: []goagent.ToolCall{weatherCall("call-1")}}}},
				scriptedTool{},
				denyAllPolicy,
			),
			wantStop: goagent.StopPolicyDenied,
			wantEvents: []goagent.EventKind{
				goagent.EventToolCall,
				goagent.EventPolicyDecision,
				goagent.EventStop,
			},
		},
		{
			name:    "step limit",
			ctx:     context.Background(),
			request: goagent.RunRequest{Input: "Weather in Austin?", MaxSteps: 1},
			runtime: newScriptedRuntime(
				[]scriptedTurn{
					{result: goagent.TurnResult{ToolCalls: []goagent.ToolCall{weatherCall("call-1")}}},
					{result: goagent.TurnResult{
						Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "unreached"},
						StopReason: goagent.StopComplete,
					}},
				},
				scriptedTool{},
				allowAllPolicy,
			),
			wantStop: goagent.StopStepLimit,
			wantEvents: []goagent.EventKind{
				goagent.EventToolCall,
				goagent.EventPolicyDecision,
				goagent.EventToolResult,
				goagent.EventStop,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.runtime.Run(tt.ctx, tt.request)
			if err != nil {
				t.Fatal(err)
			}
			if result.StopReason != tt.wantStop {
				t.Fatalf("StopReason = %q, want %q", result.StopReason, tt.wantStop)
			}
			assertEventKinds(t, result.Events, tt.wantEvents)
			assertOrderedCorrelatedEvents(t, result.Events)
		})
	}
}

func TestRetrySemanticsAreStartedInRoadmap(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}

	contents := string(readme)
	for _, want := range []string{
		"| Retries",
		"Runtime retry policy and retry events",
		"Started",
		"Observable model/runtime/tool retry exists",
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("README roadmap does not describe started retry semantics; missing %q", want)
		}
	}
}

type scriptedRuntime struct {
	runID  string
	model  *scriptedModel
	tools  map[string]goagent.Tool
	policy goagent.Policy
}

func newScriptedRuntime(turns []scriptedTurn, tool goagent.Tool, policy goagent.Policy) *scriptedRuntime {
	return &scriptedRuntime{
		runID:  "run-1",
		model:  &scriptedModel{turns: turns},
		tools:  map[string]goagent.Tool{tool.Name(): tool},
		policy: policy,
	}
}

func (r *scriptedRuntime) Run(ctx context.Context, request goagent.RunRequest) (goagent.RunResult, error) {
	var (
		events  []goagent.Event
		seq     int64
		text    string
		step    int
		message = goagent.Message{Role: goagent.RoleUser, Content: request.Input}
	)

	emit := func(event goagent.Event) {
		seq++
		event.Sequence = seq
		event.RunID = r.runID
		events = append(events, event)
	}
	finish := func(reason goagent.StopReason) (goagent.RunResult, error) {
		emit(goagent.Event{Kind: goagent.EventStop, StopReason: reason})
		return goagent.RunResult{Text: text, StopReason: reason, Events: events}, nil
	}
	fail := func(kind goagent.StopReason, event goagent.Event) (goagent.RunResult, error) {
		event.Kind = goagent.EventError
		emit(event)
		return finish(kind)
	}

	for {
		if err := ctx.Err(); err != nil {
			return fail(goagent.StopCanceled, goagent.Event{Err: err})
		}
		if request.MaxSteps > 0 && step >= request.MaxSteps {
			return finish(goagent.StopStepLimit)
		}

		step++
		turnID := fmt.Sprintf("turn-%d", step)
		turn, err := r.model.Turn(ctx, goagent.TurnRequest{
			Messages: []goagent.Message{message},
			Tools:    toolSpecs(r.tools),
			Session:  request.Session,
		})
		if err != nil {
			return fail(goagent.StopModelError, goagent.Event{TurnID: turnID, Err: err})
		}

		for _, call := range turn.ToolCalls {
			emit(goagent.Event{Kind: goagent.EventToolCall, TurnID: turnID, ToolCallID: call.ID, ToolCall: call})

			decision, err := r.policy.Decide(ctx, goagent.Decision{ToolCall: call, Tool: toolSpec(r.tools[call.Name])})
			if err != nil {
				return fail(goagent.StopPolicyDenied, goagent.Event{TurnID: turnID, ToolCallID: call.ID, Err: err})
			}
			emit(goagent.Event{Kind: goagent.EventPolicyDecision, TurnID: turnID, ToolCallID: call.ID})
			if !decision.Allowed {
				return finish(goagent.StopPolicyDenied)
			}

			result, err := r.tools[call.Name].Call(ctx, call)
			if err != nil {
				return fail(goagent.StopToolError, goagent.Event{TurnID: turnID, ToolCallID: call.ID, Err: err})
			}
			emit(goagent.Event{Kind: goagent.EventToolResult, TurnID: turnID, ToolCallID: call.ID, ToolResult: result})
		}

		if turn.Message.Content != "" {
			text += turn.Message.Content
			emit(goagent.Event{Kind: goagent.EventTextDelta, TurnID: turnID, Text: turn.Message.Content, Message: turn.Message})
		}
		if len(turn.ToolCalls) == 0 {
			if turn.StopReason == "" {
				turn.StopReason = goagent.StopComplete
			}
			return finish(turn.StopReason)
		}
	}
}

func (r *scriptedRuntime) Stream(context.Context, goagent.RunRequest) (<-chan goagent.Event, error) {
	panic("streaming behavior is specified by event ordering, not this test helper")
}

type scriptedTurn struct {
	result goagent.TurnResult
	err    error
}

type scriptedModel struct {
	turns []scriptedTurn
	next  int
}

func (m *scriptedModel) Turn(context.Context, goagent.TurnRequest) (goagent.TurnResult, error) {
	if m.next >= len(m.turns) {
		return goagent.TurnResult{}, errors.New("unexpected model turn")
	}
	turn := m.turns[m.next]
	m.next++
	return turn.result, turn.err
}

type scriptedTool struct {
	err error
}

func (scriptedTool) Name() string { return "weather" }

func (scriptedTool) Description() string { return "Get weather for a city." }

func (scriptedTool) Schema() goagent.ToolSchema { return goagent.ToolSchema{"type": "object"} }

func (t scriptedTool) Call(_ context.Context, call goagent.ToolCall) (goagent.ToolResult, error) {
	if t.err != nil {
		return goagent.ToolResult{}, t.err
	}
	var input struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return goagent.ToolResult{}, err
	}
	return goagent.ToolResult{CallID: call.ID, Name: call.Name, Content: "clear in " + input.City}, nil
}

var allowAllPolicy = goagent.PolicyFunc(func(context.Context, goagent.Decision) (goagent.PolicyDecision, error) {
	return goagent.PolicyDecision{Allowed: true}, nil
})

var denyAllPolicy = goagent.PolicyFunc(func(context.Context, goagent.Decision) (goagent.PolicyDecision, error) {
	return goagent.PolicyDecision{Allowed: false, Reason: "denied by test policy"}, nil
})

func weatherCall(id string) goagent.ToolCall {
	return goagent.ToolCall{ID: id, Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func assertEventKinds(t *testing.T, events []goagent.Event, want []goagent.EventKind) {
	t.Helper()

	got := make([]goagent.EventKind, 0, len(events))
	for _, event := range events {
		got = append(got, event.Kind)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("event kinds = %v, want %v", got, want)
	}
}

func assertOrderedCorrelatedEvents(t *testing.T, events []goagent.Event) {
	t.Helper()

	if len(events) == 0 {
		t.Fatal("no events emitted")
	}
	if events[len(events)-1].Kind != goagent.EventStop {
		t.Fatalf("last event kind = %q, want %q", events[len(events)-1].Kind, goagent.EventStop)
	}

	toolCalls := map[string]bool{}
	for i, event := range events {
		if event.Sequence != int64(i+1) {
			t.Fatalf("event %d sequence = %d, want %d", i, event.Sequence, i+1)
		}
		if event.RunID == "" {
			t.Fatalf("event %d has empty RunID", i)
		}

		switch event.Kind {
		case goagent.EventToolCall:
			if event.ToolCallID == "" || event.ToolCall.ID != event.ToolCallID {
				t.Fatalf("tool call event %d is not correlated: %+v", i, event)
			}
			toolCalls[event.ToolCallID] = true
		case goagent.EventPolicyDecision, goagent.EventToolResult:
			if !toolCalls[event.ToolCallID] {
				t.Fatalf("event %d references unknown tool call %q", i, event.ToolCallID)
			}
		}
	}
}

func toolSpecs(tools map[string]goagent.Tool) []goagent.ToolSpec {
	specs := make([]goagent.ToolSpec, 0, len(tools))
	for _, tool := range tools {
		specs = append(specs, toolSpec(tool))
	}
	return specs
}

func toolSpec(tool goagent.Tool) goagent.ToolSpec {
	return goagent.ToolSpec{
		Name:        tool.Name(),
		Description: tool.Description(),
		Schema:      tool.Schema(),
	}
}
