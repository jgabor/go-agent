package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
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
		agent      goagent.Agent
		wantStop   goagent.StopReason
		wantEvents []goagent.EventKind
		assert     func(*testing.T, goagent.RunResult, goagent.Agent)
	}{
		{
			name:    "normal completion",
			ctx:     context.Background(),
			request: goagent.RunRequest{Input: "Should I bring a jacket?"},
			agent: goagent.Agent{Model: &recordingModel{turns: []goagent.TurnResult{{
				Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Bring a jacket."},
				StopReason: goagent.StopComplete,
			}}}},
			wantStop:   goagent.StopComplete,
			wantEvents: []goagent.EventKind{goagent.EventTextDelta, goagent.EventStop},
		},
		{
			name:    "tool call dispatch",
			ctx:     context.Background(),
			request: goagent.RunRequest{Input: "Weather in Austin?"},
			agent: goagent.Agent{
				Model: &recordingModel{turns: []goagent.TurnResult{
					{ToolCalls: []goagent.ToolCall{weatherCall("call-1")}},
					{
						Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Clear in Austin."},
						StopReason: goagent.StopComplete,
					},
				}},
				Tools: []goagent.Tool{namedTool{name: "weather"}},
			},
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
			agent: goagent.Agent{
				Model: &recordingModel{turns: []goagent.TurnResult{{ToolCalls: []goagent.ToolCall{weatherCall("call-1")}}}},
				Tools: []goagent.Tool{namedTool{name: "weather", err: errors.New("tool failed")}},
			},
			wantStop: goagent.StopToolError,
			wantEvents: []goagent.EventKind{
				goagent.EventToolCall,
				goagent.EventPolicyDecision,
				goagent.EventError,
				goagent.EventStop,
			},
		},
		{
			name:       "model error",
			ctx:        context.Background(),
			request:    goagent.RunRequest{Input: "Weather in Austin?"},
			agent:      goagent.Agent{Model: &recordingModel{err: errors.New("model failed")}},
			wantStop:   goagent.StopModelError,
			wantEvents: []goagent.EventKind{goagent.EventError, goagent.EventStop},
		},
		{
			name:       "cancellation",
			ctx:        canceledContext(),
			request:    goagent.RunRequest{Input: "Weather in Austin?"},
			agent:      goagent.Agent{Model: &recordingModel{turns: []goagent.TurnResult{{Message: goagent.Message{Content: "unreached"}}}}},
			wantStop:   goagent.StopCanceled,
			wantEvents: []goagent.EventKind{goagent.EventError, goagent.EventStop},
			assert: func(t *testing.T, _ goagent.RunResult, agent goagent.Agent) {
				t.Helper()
				model := agent.Model.(*recordingModel)
				if len(model.requests) != 0 {
					t.Fatalf("model turns = %d, want 0", len(model.requests))
				}
			},
		},
		{
			name:    "run start policy denial",
			ctx:     context.Background(),
			request: goagent.RunRequest{Input: "Weather in Austin?"},
			agent: goagent.Agent{
				Model:  &recordingModel{turns: []goagent.TurnResult{{ToolCalls: []goagent.ToolCall{weatherCall("call-1")}}}},
				Tools:  []goagent.Tool{namedTool{name: "weather"}},
				Policy: denyAllPolicy,
			},
			wantStop: goagent.StopPolicyDenied,
			wantEvents: []goagent.EventKind{
				goagent.EventPolicyDecision,
				goagent.EventStop,
			},
			assert: func(t *testing.T, result goagent.RunResult, agent goagent.Agent) {
				t.Helper()
				if result.Events[0].Decision.Kind != goagent.DecisionRunStart {
					t.Fatalf("first policy decision kind = %q, want %q", result.Events[0].Decision.Kind, goagent.DecisionRunStart)
				}
				model := agent.Model.(*recordingModel)
				if len(model.requests) != 0 {
					t.Fatalf("model turns = %d, want 0", len(model.requests))
				}
			},
		},
		{
			name:    "step limit",
			ctx:     context.Background(),
			request: goagent.RunRequest{Input: "Weather in Austin?", MaxSteps: 1},
			agent: goagent.Agent{
				Model: &recordingModel{turns: []goagent.TurnResult{
					{ToolCalls: []goagent.ToolCall{weatherCall("call-1")}},
					{
						Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "unreached"},
						StopReason: goagent.StopComplete,
					},
				}},
				Tools: []goagent.Tool{namedTool{name: "weather"}},
			},
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
			runner, err := goagent.NewRunner(tt.agent)
			if err != nil {
				t.Fatal(err)
			}

			result, err := runner.Run(tt.ctx, tt.request)
			if err != nil {
				t.Fatal(err)
			}
			if result.StopReason != tt.wantStop {
				t.Fatalf("StopReason = %q, want %q", result.StopReason, tt.wantStop)
			}
			assertEventKinds(t, result.Events, tt.wantEvents)
			assertOrderedCorrelatedEvents(t, result.Events)
			if tt.assert != nil {
				tt.assert(t, result, tt.agent)
			}
		})
	}
}

func TestRuntimeBehaviorStreamUsesRunnerEvents(t *testing.T) {
	runner, err := goagent.NewRunner(goagent.Agent{Model: &recordingModel{turns: []goagent.TurnResult{{
		Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Bring a jacket."},
		StopReason: goagent.StopComplete,
	}}}})
	if err != nil {
		t.Fatal(err)
	}

	events, err := runner.Stream(context.Background(), goagent.RunRequest{Input: "Should I bring a jacket?"})
	if err != nil {
		t.Fatal(err)
	}

	var got []goagent.Event
	for event := range events {
		got = append(got, event)
	}
	assertEventKinds(t, got, []goagent.EventKind{goagent.EventTextDelta, goagent.EventStop})
	assertOrderedCorrelatedEvents(t, got)
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
		case goagent.EventPolicyDecision:
			if event.ToolCallID != "" && !toolCalls[event.ToolCallID] {
				t.Fatalf("event %d references unknown tool call %q", i, event.ToolCallID)
			}
		case goagent.EventToolResult:
			if !toolCalls[event.ToolCallID] {
				t.Fatalf("event %d references unknown tool call %q", i, event.ToolCallID)
			}
		}
	}
}
