package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestRuntimeHistoryCorrelatesRunStreamSinkAndFinalResult(t *testing.T) {
	runResult, runObserved := runEventHistoryScenario(t, false)
	streamEvents, streamObserved := streamEventHistoryScenario(t)

	wantKinds := []goagent.EventKind{
		goagent.EventPolicyDecision,
		goagent.EventToolCall,
		goagent.EventPolicyDecision,
		goagent.EventRetry,
		goagent.EventRetry,
		goagent.EventPolicyDecision,
		goagent.EventRetry,
		goagent.EventRetry,
		goagent.EventPolicyDecision,
		goagent.EventToolResult,
		goagent.EventTextDelta,
		goagent.EventStop,
	}
	assertEventKinds(t, runResult.Events, wantKinds)
	assertEventKinds(t, streamEvents, wantKinds)
	assertSameEventShape(t, runResult.Events, streamEvents)
	assertSameEventShape(t, runObserved, runResult.Events)
	assertSameEventShape(t, streamObserved, streamEvents)
	assertRuntimeHistoryResultLinkage(t, runResult)
}

func TestRuntimeHistorySinkPanicStaysObservational(t *testing.T) {
	result, observed := runEventHistoryScenario(t, true)

	if result.StopReason != goagent.StopComplete || result.Text != "Recovered." {
		t.Fatalf("RunResult = %+v", result)
	}
	if len(observed) != 0 {
		t.Fatalf("panic-only sink should not append observations: %+v", observed)
	}
	assertRetryEvents(t, result.Events, []retryEventSpec{
		{target: goagent.RetryTargetTool, reason: goagent.RetryReasonToolError, attempt: 1, maxAttempts: 2, outcome: goagent.RetryOutcomeFailed, toolCallID: "call-1", toolName: "weather"},
		{target: goagent.RetryTargetTool, reason: goagent.RetryReasonToolError, attempt: 2, maxAttempts: 2, outcome: goagent.RetryOutcomeConsidered, toolCallID: "call-1", toolName: "weather"},
		{target: goagent.RetryTargetTool, reason: goagent.RetryReasonToolError, attempt: 2, maxAttempts: 2, outcome: goagent.RetryOutcomeAttempted, toolCallID: "call-1", toolName: "weather"},
		{target: goagent.RetryTargetTool, reason: goagent.RetryReasonToolError, attempt: 2, maxAttempts: 2, outcome: goagent.RetryOutcomeSucceeded, toolCallID: "call-1", toolName: "weather"},
	})
}

func TestRuntimeHistoryTerminalEventsAreReconstructable(t *testing.T) {
	modelErr := errors.New("model failed")
	runner, err := goagent.NewRunner(goagent.Agent{Model: &recordingModel{err: modelErr}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Go"})
	if err != nil {
		t.Fatal(err)
	}

	if result.StopReason != goagent.StopModelError {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, goagent.StopModelError)
	}
	if len(result.Events) != 2 {
		t.Fatalf("events = %+v, want error then stop", result.Events)
	}
	errorEvent, stopEvent := result.Events[0], result.Events[1]
	if errorEvent.Kind != goagent.EventError || !errors.Is(errorEvent.Err, modelErr) || errorEvent.TurnID != "turn-1" {
		t.Fatalf("error event is not reconstructable: %+v", errorEvent)
	}
	if stopEvent.Kind != goagent.EventStop || stopEvent.StopReason != result.StopReason {
		t.Fatalf("stop event = %+v, result stop = %q", stopEvent, result.StopReason)
	}
	assertOrderedCorrelatedEvents(t, result.Events)
}

func runEventHistoryScenario(t *testing.T, panicSink bool) (goagent.RunResult, []goagent.Event) {
	t.Helper()

	runner, observed := newEventHistoryRunner(t, panicSink)
	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}
	return result, observed.events
}

func streamEventHistoryScenario(t *testing.T) ([]goagent.Event, []goagent.Event) {
	t.Helper()

	runner, observed := newEventHistoryRunner(t, false)
	stream, err := runner.Stream(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}
	var events []goagent.Event
	for event := range stream {
		events = append(events, event)
	}
	return events, observed.events
}

func newEventHistoryRunner(t *testing.T, panicSink bool) (goagent.Runner, *eventCollector) {
	t.Helper()

	model := &flakyModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Recovered."}, StopReason: goagent.StopComplete},
	}}
	var calls int
	tool := safeFailingOnceTool(t, &calls, errors.New("weather timeout"))
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	observed := &eventCollector{}
	sink := goagent.EventSinkFunc(func(context.Context, goagent.Event) {
		panic("sink failed")
	})
	if !panicSink {
		sink = goagent.EventSinkFunc(func(_ context.Context, event goagent.Event) {
			observed.events = append(observed.events, event)
		})
	}
	runner, err := goagent.NewRunner(goagent.Agent{
		Model:      model,
		Tools:      []goagent.Tool{tool},
		Policy:     policy,
		EventSinks: []goagent.EventSink{sink},
		Retry:      goagent.RetryPolicy{MaxAttempts: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner, observed
}

type eventCollector struct {
	events []goagent.Event
}

func assertSameEventShape(t *testing.T, got []goagent.Event, want []goagent.Event) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d: got=%+v want=%+v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i].Kind != want[i].Kind || got[i].Sequence != want[i].Sequence || got[i].TurnID != want[i].TurnID || got[i].ToolCallID != want[i].ToolCallID || got[i].StopReason != want[i].StopReason {
			t.Fatalf("event %d shape = %+v, want %+v", i, got[i], want[i])
		}
		if got[i].RunID == "" {
			t.Fatalf("event %d has empty RunID", i)
		}
		if got[i].Kind == goagent.EventRetry && got[i].Retry != want[i].Retry {
			t.Fatalf("retry event %d = %+v, want %+v", i, got[i].Retry, want[i].Retry)
		}
		if got[i].Kind == goagent.EventPolicyDecision && got[i].Decision.Kind != want[i].Decision.Kind {
			t.Fatalf("policy event %d kind = %q, want %q", i, got[i].Decision.Kind, want[i].Decision.Kind)
		}
	}
	assertOrderedCorrelatedEvents(t, got)
}

func assertRuntimeHistoryResultLinkage(t *testing.T, result goagent.RunResult) {
	t.Helper()

	if result.StopReason != goagent.StopComplete || result.Text != "Recovered." {
		t.Fatalf("RunResult = %+v", result)
	}
	if last := result.Events[len(result.Events)-1]; last.Kind != goagent.EventStop || last.StopReason != result.StopReason {
		t.Fatalf("final event = %+v, result stop = %q", last, result.StopReason)
	}
	if !slices.ContainsFunc(result.Events, func(event goagent.Event) bool {
		return event.Kind == goagent.EventTextDelta && event.Text == result.Text && event.Message.Content == result.Text
	}) {
		t.Fatalf("result text %q not linked to text event: %+v", result.Text, result.Events)
	}
	assertRetryEvents(t, result.Events, []retryEventSpec{
		{target: goagent.RetryTargetTool, reason: goagent.RetryReasonToolError, attempt: 1, maxAttempts: 2, outcome: goagent.RetryOutcomeFailed, toolCallID: "call-1", toolName: "weather"},
		{target: goagent.RetryTargetTool, reason: goagent.RetryReasonToolError, attempt: 2, maxAttempts: 2, outcome: goagent.RetryOutcomeConsidered, toolCallID: "call-1", toolName: "weather"},
		{target: goagent.RetryTargetTool, reason: goagent.RetryReasonToolError, attempt: 2, maxAttempts: 2, outcome: goagent.RetryOutcomeAttempted, toolCallID: "call-1", toolName: "weather"},
		{target: goagent.RetryTargetTool, reason: goagent.RetryReasonToolError, attempt: 2, maxAttempts: 2, outcome: goagent.RetryOutcomeSucceeded, toolCallID: "call-1", toolName: "weather"},
	})
}
