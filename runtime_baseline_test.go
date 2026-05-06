package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestRuntimeBaselineClassifiesRetryTargets(t *testing.T) {
	t.Run("model turn failure is a retry target", func(t *testing.T) {
		modelErr := errors.New("model unavailable")
		model := &flakyModel{errors: []error{modelErr}, turns: []goagent.TurnResult{{Message: goagent.Message{Content: "recovered"}}}}
		runner, err := goagent.NewRunner(goagent.Agent{Model: model, Retry: goagent.RetryPolicy{MaxAttempts: 2}})
		if err != nil {
			t.Fatal(err)
		}

		result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "try"})
		if err != nil {
			t.Fatal(err)
		}

		if result.StopReason != goagent.StopComplete || model.calls != 2 {
			t.Fatalf("result=%+v model calls=%d", result, model.calls)
		}
		assertRetryTargetKinds(t, result.Events, []goagent.RetryTargetKind{goagent.RetryTargetModel})
	})

	t.Run("session-store load failure is a runtime retry target", func(t *testing.T) {
		storeErr := errors.New("store unavailable")
		store := &flakySessionStore{loadErrs: []error{storeErr}}
		model := &flakyModel{turns: []goagent.TurnResult{{Message: goagent.Message{Content: "loaded"}}}}
		runner, err := goagent.NewRunner(goagent.Agent{Model: model, SessionStore: store, Retry: goagent.RetryPolicy{MaxAttempts: 2}})
		if err != nil {
			t.Fatal(err)
		}

		result, err := runner.Run(context.Background(), goagent.RunRequest{SessionID: "session-1", Input: "load"})
		if err != nil {
			t.Fatal(err)
		}

		if result.StopReason != goagent.StopComplete || store.loads != 2 || model.calls != 1 {
			t.Fatalf("result=%+v loads=%d model calls=%d", result, store.loads, model.calls)
		}
		assertRetryTargetKinds(t, result.Events, []goagent.RetryTargetKind{goagent.RetryTargetRuntime})
	})

	t.Run("retry-safe tool failure is a tool retry target", func(t *testing.T) {
		toolErr := errors.New("tool unavailable")
		model := &flakyModel{turns: []goagent.TurnResult{
			{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
			{Message: goagent.Message{Content: "done"}, StopReason: goagent.StopComplete},
		}}
		var calls int
		tool, err := goagent.NewToolFromDefinition(goagent.ToolDefinition{
			Name:        "weather",
			Description: "Get weather.",
			Schema:      goagent.ToolSchema{"type": "object"},
			Function: func(context.Context, string) (string, error) {
				calls++
				if calls == 1 {
					return "", toolErr
				}
				return "clear", nil
			},
			Safety: goagent.ToolSafety{ReadOnly: true, Retryable: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Retry: goagent.RetryPolicy{MaxAttempts: 2}})
		if err != nil {
			t.Fatal(err)
		}

		result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
		if err != nil {
			t.Fatal(err)
		}

		if result.StopReason != goagent.StopComplete || calls != 2 {
			t.Fatalf("result=%+v tool calls=%d", result, calls)
		}
		assertRetryTargetKinds(t, result.Events, []goagent.RetryTargetKind{goagent.RetryTargetTool})
	})
}

func TestRuntimeBaselineClassifiesTerminalNonRetryBehavior(t *testing.T) {
	tests := []struct {
		name          string
		ctx           context.Context
		request       goagent.RunRequest
		agent         goagent.Agent
		wantStop      goagent.StopReason
		wantModelCall int
	}{
		{
			name: "unknown tool remains terminal",
			ctx:  context.Background(),
			agent: goagent.Agent{Model: &recordingModel{turns: []goagent.TurnResult{{
				ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "missing", Input: json.RawMessage(`{}`)}},
			}}}},
			wantStop:      goagent.StopToolError,
			wantModelCall: 1,
		},
		{
			name: "policy denial remains terminal",
			ctx:  context.Background(),
			agent: goagent.Agent{
				Model: &recordingModel{turns: []goagent.TurnResult{{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{}`)}}}}},
				Tools: []goagent.Tool{namedTool{name: "weather"}},
				Policy: goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
					return goagent.PolicyDecision{Allowed: decision.Kind != goagent.DecisionToolCall}, nil
				}),
			},
			wantStop:      goagent.StopPolicyDenied,
			wantModelCall: 1,
		},
		{
			name:          "cancellation remains terminal",
			ctx:           canceledContext(),
			agent:         goagent.Agent{Model: &recordingModel{turns: []goagent.TurnResult{{Message: goagent.Message{Content: "unreached"}}}}},
			wantStop:      goagent.StopCanceled,
			wantModelCall: 0,
		},
		{
			name:    "step limit remains terminal",
			ctx:     context.Background(),
			request: goagent.RunRequest{MaxSteps: 1},
			agent: goagent.Agent{
				Model: &recordingModel{turns: []goagent.TurnResult{
					{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{}`)}}},
					{Message: goagent.Message{Content: "unreached"}},
				}},
				Tools: []goagent.Tool{namedTool{name: "weather"}},
			},
			wantStop:      goagent.StopStepLimit,
			wantModelCall: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.agent.Retry = goagent.RetryPolicy{MaxAttempts: 3}
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
			if got := countRetryEvents(result.Events); got != 0 {
				t.Fatalf("retry events = %d, want 0: %+v", got, result.Events)
			}
			if model, ok := tt.agent.Model.(*recordingModel); ok && len(model.requests) != tt.wantModelCall {
				t.Fatalf("model calls = %d, want %d", len(model.requests), tt.wantModelCall)
			}
		})
	}
}

func TestRuntimeBaselineSessionLoadRetryExhaustionReturnsErrorAndEvents(t *testing.T) {
	storeErr := errors.New("store unavailable")
	store := &flakySessionStore{loadErrs: []error{storeErr, storeErr}}
	model := &flakyModel{turns: []goagent.TurnResult{{Message: goagent.Message{Content: "unreached"}}}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, SessionStore: store, Retry: goagent.RetryPolicy{MaxAttempts: 2}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{SessionID: "session-1", Input: "load"})
	if !errors.Is(err, storeErr) {
		t.Fatalf("Run error = %v, want %v", err, storeErr)
	}
	if result.StopReason != goagent.StopRetryExhausted || model.calls != 0 {
		t.Fatalf("result=%+v model calls=%d", result, model.calls)
	}
	assertRetryOutcomes(t, result.Events, []goagent.RetryOutcome{
		goagent.RetryOutcomeFailed,
		goagent.RetryOutcomeConsidered,
		goagent.RetryOutcomeAttempted,
		goagent.RetryOutcomeFailed,
		goagent.RetryOutcomeExhausted,
	})
	assertEventKinds(t, result.Events[len(result.Events)-2:], []goagent.EventKind{goagent.EventError, goagent.EventStop})
}

func assertRetryTargetKinds(t *testing.T, events []goagent.Event, want []goagent.RetryTargetKind) {
	t.Helper()

	seen := map[goagent.RetryTargetKind]bool{}
	for _, event := range events {
		if event.Kind == goagent.EventRetry {
			seen[event.Retry.Target.Kind] = true
		}
	}
	for _, kind := range want {
		if !seen[kind] {
			t.Fatalf("missing retry target %q in events: %+v", kind, events)
		}
	}
}

func countRetryEvents(events []goagent.Event) int {
	var count int
	for _, event := range events {
		if event.Kind == goagent.EventRetry {
			count++
		}
	}
	return count
}
