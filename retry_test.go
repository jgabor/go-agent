package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	goagent "github.com/jgabor/go-agent"
)

func TestRunnerRetriesModelErrorWithObservablePolicyAndAttempts(t *testing.T) {
	modelErr := errors.New("model unavailable")
	model := &flakyModel{errors: []error{modelErr}, turns: []goagent.TurnResult{{Message: goagent.Message{Content: "Recovered."}}}}
	var retryDecisions []goagent.RetryContext
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionRetry {
			retryDecisions = append(retryDecisions, decision.Retry)
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Policy: policy, Retry: goagent.RetryPolicy{MaxAttempts: 3}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "try"})
	if err != nil {
		t.Fatal(err)
	}

	if result.StopReason != goagent.StopComplete || result.Text != "Recovered." {
		t.Fatalf("RunResult = %+v", result)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
	if len(retryDecisions) != 1 || retryDecisions[0].Attempt != 2 || retryDecisions[0].MaxAttempts != 3 || !errors.Is(retryDecisions[0].Err, modelErr) {
		t.Fatalf("retry decisions = %+v", retryDecisions)
	}
	assertRetryOutcomes(t, result.Events, []goagent.RetryOutcome{
		goagent.RetryOutcomeFailed,
		goagent.RetryOutcomeConsidered,
		goagent.RetryOutcomeAttempted,
		goagent.RetryOutcomeSucceeded,
	})
	if !hasRetryPolicyEvent(result.Events, goagent.RetryTargetModel) {
		t.Fatalf("missing retry policy event: %+v", result.Events)
	}
}

func TestRunnerRetryPolicyDenialStopsExplicitly(t *testing.T) {
	modelErr := errors.New("model unavailable")
	model := &flakyModel{errors: []error{modelErr, modelErr}}
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionRetry {
			return goagent.PolicyDecision{Allowed: false, Reason: "no retry"}, nil
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Policy: policy, Retry: goagent.RetryPolicy{MaxAttempts: 3}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "try"})
	if err != nil {
		t.Fatal(err)
	}

	if result.StopReason != goagent.StopPolicyDenied {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, goagent.StopPolicyDenied)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
	assertRetryOutcomes(t, result.Events, []goagent.RetryOutcome{goagent.RetryOutcomeFailed, goagent.RetryOutcomeConsidered, goagent.RetryOutcomeDenied})
	assertEventKinds(t, result.Events[len(result.Events)-2:], []goagent.EventKind{goagent.EventError, goagent.EventStop})
}

func TestRunnerRetryPolicyConstraintBoundsAttempts(t *testing.T) {
	modelErr := errors.New("model unavailable")
	model := &flakyModel{errors: []error{modelErr, modelErr}, turns: []goagent.TurnResult{{Message: goagent.Message{Content: "unreached"}}}}
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionRetry {
			return goagent.PolicyDecision{Allowed: true, Retry: goagent.RetryPolicy{MaxAttempts: 1}}, nil
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Policy: policy, Retry: goagent.RetryPolicy{MaxAttempts: 3}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "try"})
	if err != nil {
		t.Fatal(err)
	}

	if result.StopReason != goagent.StopRetryExhausted {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, goagent.StopRetryExhausted)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
	assertRetryOutcomes(t, result.Events, []goagent.RetryOutcome{goagent.RetryOutcomeFailed, goagent.RetryOutcomeConsidered, goagent.RetryOutcomeConstrained, goagent.RetryOutcomeExhausted})
}

func TestRunnerRuntimeRetryCoversSessionLoad(t *testing.T) {
	storeErr := errors.New("store temporarily unavailable")
	store := &flakySessionStore{loadErrs: []error{storeErr}}
	model := &flakyModel{turns: []goagent.TurnResult{{Message: goagent.Message{Content: "Loaded."}}}}
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
	if !hasRetryPolicyEvent(result.Events, goagent.RetryTargetRuntime) {
		t.Fatalf("missing runtime retry policy event: %+v", result.Events)
	}
	assertRetryOutcomes(t, result.Events, []goagent.RetryOutcome{goagent.RetryOutcomeFailed, goagent.RetryOutcomeConsidered, goagent.RetryOutcomeAttempted, goagent.RetryOutcomeSucceeded})
}

func TestRunnerRetriesSafeToolFailureWithPolicyContextAndEvents(t *testing.T) {
	toolErr := errors.New("weather timeout")
	model := &flakyModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Content: "Recovered."}, StopReason: goagent.StopComplete},
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
	var retryDecision goagent.RetryContext
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionRetry {
			retryDecision = decision.Retry
			if decision.Tool.Name != "weather" || !decision.Tool.Safety.Retryable || decision.ToolCall.ID != "call-1" {
				t.Fatalf("retry decision missing tool context: %+v", decision)
			}
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Policy: policy, Retry: goagent.RetryPolicy{MaxAttempts: 2}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}

	if result.StopReason != goagent.StopComplete || result.Text != "Recovered." || calls != 2 {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
	if retryDecision.Target.Kind != goagent.RetryTargetTool || retryDecision.Tool.Name != "weather" || retryDecision.ToolCall.ID != "call-1" || !errors.Is(retryDecision.Err, toolErr) {
		t.Fatalf("retry context = %+v", retryDecision)
	}
	assertRetryOutcomes(t, result.Events, []goagent.RetryOutcome{goagent.RetryOutcomeFailed, goagent.RetryOutcomeConsidered, goagent.RetryOutcomeAttempted, goagent.RetryOutcomeSucceeded})
	assertToolRetryEventPath(t, result.Events, []goagent.EventKind{goagent.EventToolCall, goagent.EventRetry, goagent.EventRetry, goagent.EventPolicyDecision, goagent.EventRetry, goagent.EventRetry, goagent.EventToolResult, goagent.EventTextDelta, goagent.EventStop})
}

func TestRunnerBlocksUnsafeToolRetryByDefault(t *testing.T) {
	toolErr := errors.New("posted charge uncertain")
	model := &flakyModel{turns: []goagent.TurnResult{{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "charge", Input: json.RawMessage(`{"amount":"10"}`)}}}}}
	var calls, retryDecisions int
	tool, err := goagent.NewTool("charge", "Charge a card.", func(context.Context, string) (string, error) {
		calls++
		return "", toolErr
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionRetry {
			retryDecisions++
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Policy: policy, Retry: goagent.RetryPolicy{MaxAttempts: 2}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Charge"})
	if err != nil {
		t.Fatal(err)
	}

	if result.StopReason != goagent.StopToolError || calls != 1 || retryDecisions != 0 {
		t.Fatalf("result=%+v calls=%d retry decisions=%d", result, calls, retryDecisions)
	}
	assertRetryOutcomes(t, result.Events, []goagent.RetryOutcome{goagent.RetryOutcomeFailed, goagent.RetryOutcomeBlocked})
	blocked := retryEventWithOutcome(result.Events, goagent.RetryOutcomeBlocked)
	if blocked.Retry.Reason != goagent.RetryReasonToolRetryBlocked || blocked.Tool.Name != "charge" || blocked.Tool.Safety.Retryable {
		t.Fatalf("blocked retry event = %+v", blocked)
	}
}

func TestRunnerToolRetryPolicyDenialStopsExplicitly(t *testing.T) {
	toolErr := errors.New("tool failed")
	model := &flakyModel{turns: []goagent.TurnResult{{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}}}}
	var calls int
	tool := safeFailingTool(t, &calls, toolErr)
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionRetry {
			return goagent.PolicyDecision{Allowed: false, Reason: "do not repeat"}, nil
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Policy: policy, Retry: goagent.RetryPolicy{MaxAttempts: 3}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}

	if result.StopReason != goagent.StopPolicyDenied || calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
	assertRetryOutcomes(t, result.Events, []goagent.RetryOutcome{goagent.RetryOutcomeFailed, goagent.RetryOutcomeConsidered, goagent.RetryOutcomeDenied})
	assertEventKinds(t, result.Events[len(result.Events)-2:], []goagent.EventKind{goagent.EventError, goagent.EventStop})
}

func TestRunnerToolRetryPolicyConstraintCanExhaustBeforeRetry(t *testing.T) {
	toolErr := errors.New("tool failed")
	model := &flakyModel{turns: []goagent.TurnResult{{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}}}}
	var calls int
	tool := safeFailingTool(t, &calls, toolErr)
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionRetry {
			return goagent.PolicyDecision{Allowed: true, Retry: goagent.RetryPolicy{MaxAttempts: 1}}, nil
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Policy: policy, Retry: goagent.RetryPolicy{MaxAttempts: 3}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}

	if result.StopReason != goagent.StopRetryExhausted || calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
	assertRetryOutcomes(t, result.Events, []goagent.RetryOutcome{goagent.RetryOutcomeFailed, goagent.RetryOutcomeConsidered, goagent.RetryOutcomeConstrained, goagent.RetryOutcomeExhausted})
	exhausted := retryEventWithOutcome(result.Events, goagent.RetryOutcomeExhausted)
	if exhausted.Retry.StopReason != goagent.StopRetryExhausted || exhausted.ToolCall.ID != "call-1" {
		t.Fatalf("exhausted retry event = %+v", exhausted)
	}
}

func TestRetryPolicyDecisionCarriesTypedContext(t *testing.T) {
	modelErr := errors.New("model temporarily unavailable")
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind != goagent.DecisionRetry {
			t.Fatalf("Decision.Kind = %q, want %q", decision.Kind, goagent.DecisionRetry)
		}
		if decision.Retry.Target.Kind != goagent.RetryTargetModel {
			t.Fatalf("Retry.Target.Kind = %q, want %q", decision.Retry.Target.Kind, goagent.RetryTargetModel)
		}
		if decision.Retry.Reason != goagent.RetryReasonModelError || decision.Retry.Attempt != 2 || decision.Retry.MaxAttempts != 3 || !errors.Is(decision.Retry.Err, modelErr) {
			t.Fatalf("Retry context = %+v", decision.Retry)
		}
		return goagent.PolicyDecision{
			Allowed: true,
			Reason:  "transient model failure",
			Retry: goagent.RetryPolicy{
				MaxAttempts: 2,
				Delay:       10 * time.Millisecond,
			},
		}, nil
	})

	decision, err := policy.Decide(context.Background(), goagent.Decision{
		Kind: goagent.DecisionRetry,
		Retry: goagent.RetryContext{
			Target:      goagent.RetryTarget{Kind: goagent.RetryTargetModel, TurnID: "turn-1"},
			Reason:      goagent.RetryReasonModelError,
			Attempt:     2,
			MaxAttempts: 3,
			Err:         modelErr,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed || decision.Retry.MaxAttempts != 2 || decision.Retry.Delay != 10*time.Millisecond {
		t.Fatalf("PolicyDecision = %+v", decision)
	}
}

type flakyModel struct {
	errors []error
	turns  []goagent.TurnResult
	calls  int
}

func (m *flakyModel) Turn(context.Context, goagent.TurnRequest) (goagent.TurnResult, error) {
	m.calls++
	if len(m.errors) > 0 {
		err := m.errors[0]
		m.errors = m.errors[1:]
		if err != nil {
			return goagent.TurnResult{}, err
		}
	}
	if len(m.turns) == 0 {
		return goagent.TurnResult{}, errors.New("unexpected model turn")
	}
	turn := m.turns[0]
	m.turns = m.turns[1:]
	return turn, nil
}

type flakySessionStore struct {
	loadErrs []error
	loads    int
	saved    goagent.Session
}

func (s *flakySessionStore) LoadSession(context.Context, string) (goagent.Session, error) {
	s.loads++
	if len(s.loadErrs) > 0 {
		err := s.loadErrs[0]
		s.loadErrs = s.loadErrs[1:]
		if err != nil {
			return goagent.Session{}, err
		}
	}
	return goagent.Session{}, nil
}

func (s *flakySessionStore) SaveSession(_ context.Context, session goagent.Session) error {
	s.saved = session
	return nil
}

func assertRetryOutcomes(t *testing.T, events []goagent.Event, want []goagent.RetryOutcome) {
	t.Helper()

	var got []goagent.RetryOutcome
	for _, event := range events {
		if event.Kind == goagent.EventRetry {
			got = append(got, event.Retry.Outcome)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("retry outcomes = %v, want %v", got, want)
	}
}

func hasRetryPolicyEvent(events []goagent.Event, target goagent.RetryTargetKind) bool {
	for _, event := range events {
		if event.Kind == goagent.EventPolicyDecision && event.Decision.Kind == goagent.DecisionRetry && event.Decision.Retry.Target.Kind == target {
			return true
		}
	}
	return false
}

func safeFailingTool(t *testing.T, calls *int, err error) goagent.Tool {
	t.Helper()
	tool, toolErr := goagent.NewToolFromDefinition(goagent.ToolDefinition{
		Name:        "weather",
		Description: "Get weather.",
		Schema:      goagent.ToolSchema{"type": "object"},
		Function: func(context.Context, string) (string, error) {
			*calls++
			return "", err
		},
		Safety: goagent.ToolSafety{ReadOnly: true, Retryable: true},
	})
	if toolErr != nil {
		t.Fatal(toolErr)
	}
	return tool
}

func retryEventWithOutcome(events []goagent.Event, outcome goagent.RetryOutcome) goagent.Event {
	for _, event := range events {
		if event.Kind == goagent.EventRetry && event.Retry.Outcome == outcome {
			return event
		}
	}
	return goagent.Event{}
}

func assertToolRetryEventPath(t *testing.T, events []goagent.Event, want []goagent.EventKind) {
	t.Helper()

	var got []goagent.EventKind
	for _, event := range events {
		if event.Kind == goagent.EventPolicyDecision && event.Decision.Kind != goagent.DecisionRetry {
			continue
		}
		if event.Kind == goagent.EventToolCall || event.Kind == goagent.EventRetry || event.Kind == goagent.EventPolicyDecision || event.Kind == goagent.EventToolResult || event.Kind == goagent.EventTextDelta || event.Kind == goagent.EventStop {
			got = append(got, event.Kind)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tool retry event path = %v, want %v", got, want)
	}
}

func TestRetryEventsReconstructAttemptAndTerminalOutcomes(t *testing.T) {
	target := goagent.RetryTarget{Kind: goagent.RetryTargetModel, TurnID: "turn-1"}
	events := []goagent.Event{
		{
			Kind: goagent.EventRetry,
			Retry: goagent.RetryEvent{
				Target:      target,
				Reason:      goagent.RetryReasonModelError,
				Attempt:     1,
				MaxAttempts: 2,
				Outcome:     goagent.RetryOutcomeAttempted,
			},
		},
		{
			Kind: goagent.EventRetry,
			Retry: goagent.RetryEvent{
				Target:      target,
				Reason:      goagent.RetryReasonModelError,
				Attempt:     2,
				MaxAttempts: 2,
				Outcome:     goagent.RetryOutcomeExhausted,
				StopReason:  goagent.StopRetryExhausted,
			},
		},
		{Kind: goagent.EventStop, StopReason: goagent.StopRetryExhausted},
	}

	terminal := events[1]
	if terminal.Retry.Target != target || terminal.Retry.Reason != goagent.RetryReasonModelError || terminal.Retry.Attempt != 2 || terminal.Retry.MaxAttempts != 2 {
		t.Fatalf("terminal retry event is not reconstructable: %+v", terminal)
	}
	if terminal.Retry.Outcome != goagent.RetryOutcomeExhausted || terminal.Retry.StopReason != goagent.StopRetryExhausted || events[2].StopReason != goagent.StopRetryExhausted {
		t.Fatalf("terminal retry outcome/stop reason = %+v then %+v", terminal, events[2])
	}
}

func TestRetryStopOutcomesAreTyped(t *testing.T) {
	for _, tt := range []struct {
		outcome goagent.RetryOutcome
		stop    goagent.StopReason
	}{
		{outcome: goagent.RetryOutcomeDisabled, stop: goagent.StopModelError},
		{outcome: goagent.RetryOutcomeDenied, stop: goagent.StopPolicyDenied},
		{outcome: goagent.RetryOutcomeConstrained, stop: goagent.StopRetryExhausted},
		{outcome: goagent.RetryOutcomeExhausted, stop: goagent.StopRetryExhausted},
		{outcome: goagent.RetryOutcomeCanceled, stop: goagent.StopCanceled},
	} {
		event := goagent.Event{Kind: goagent.EventRetry, Retry: goagent.RetryEvent{Outcome: tt.outcome, StopReason: tt.stop}}
		if event.Retry.Outcome == "" || event.Retry.StopReason == "" {
			t.Fatalf("retry terminal event lacks outcome or stop reason: %+v", event)
		}
	}
}

func TestToolRetryBlockEventCarriesTypedSafetyReason(t *testing.T) {
	event := goagent.Event{
		Kind: goagent.EventRetry,
		Retry: goagent.RetryEvent{
			Target: goagent.RetryTarget{
				Kind:       goagent.RetryTargetTool,
				TurnID:     "turn-1",
				ToolCallID: "call-1",
				ToolName:   "deploy.production",
			},
			Reason:  goagent.RetryReasonToolRetryBlocked,
			Attempt: 1,
			Outcome: goagent.RetryOutcomeBlocked,
		},
	}

	if event.Retry.Target.Kind != goagent.RetryTargetTool || event.Retry.Reason != goagent.RetryReasonToolRetryBlocked || event.Retry.Outcome != goagent.RetryOutcomeBlocked {
		t.Fatalf("tool retry block event = %+v", event)
	}
}
