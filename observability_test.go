package goagent_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestRunnerRejectsNilEventSink(t *testing.T) {
	_, err := goagent.NewRunner(goagent.Agent{
		Model:      &recordingModel{},
		EventSinks: []goagent.EventSink{nil},
	})
	if err == nil {
		t.Fatal("NewRunner succeeded with nil event sink")
	}
}

func TestEventSinkObservesRunEvents(t *testing.T) {
	var observed []goagent.Event
	sink := goagent.EventSinkFunc(func(ctx context.Context, event goagent.Event) {
		if ctx.Value(contextKey("trace-id")) != "trace-1" {
			t.Fatalf("sink context missing trace-id")
		}
		observed = append(observed, event)
	})
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Clear."}, StopReason: goagent.StopComplete},
	}}
	tool, err := goagent.NewTool("weather", "Get weather.", func(context.Context, string) (string, error) {
		return "clear in Austin", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, EventSinks: []goagent.EventSink{sink}})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), contextKey("trace-id"), "trace-1")
	result, err := runner.Run(ctx, goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}

	if len(observed) != len(result.Events) {
		t.Fatalf("observed %d events, result has %d", len(observed), len(result.Events))
	}
	for i := range result.Events {
		if observed[i].Kind != result.Events[i].Kind || observed[i].Sequence != result.Events[i].Sequence || observed[i].RunID != result.Events[i].RunID {
			t.Fatalf("observed event %d = %+v, result event = %+v", i, observed[i], result.Events[i])
		}
	}
	assertOrderedCorrelatedEvents(t, observed)
}

func TestEventSinkObservesStreamEvents(t *testing.T) {
	var observed []goagent.Event
	sink := goagent.EventSinkFunc(func(ctx context.Context, event goagent.Event) {
		observed = append(observed, event)
	})
	model := &recordingModel{turns: []goagent.TurnResult{{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Done."}, StopReason: goagent.StopComplete}}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, EventSinks: []goagent.EventSink{sink}})
	if err != nil {
		t.Fatal(err)
	}

	events, err := runner.Stream(context.Background(), goagent.RunRequest{Input: "Go"})
	if err != nil {
		t.Fatal(err)
	}
	var streamed []goagent.Event
	for event := range events {
		streamed = append(streamed, event)
	}

	observedKinds := eventKinds(observed)
	streamedKinds := eventKinds(streamed)
	if !slices.Equal(observedKinds, streamedKinds) {
		t.Fatalf("observed kinds = %v, streamed kinds = %v", observedKinds, streamedKinds)
	}
}

func TestEventSinkPanicDoesNotChangeRunBehavior(t *testing.T) {
	sink := goagent.EventSinkFunc(func(ctx context.Context, event goagent.Event) {
		panic("sink failed")
	})
	model := &recordingModel{turns: []goagent.TurnResult{{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Done."}, StopReason: goagent.StopComplete}}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, EventSinks: []goagent.EventSink{sink}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Go"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopComplete || result.Text != "Done." {
		t.Fatalf("RunResult = %+v", result)
	}
}

type contextKey string

func eventKinds(events []goagent.Event) []goagent.EventKind {
	kinds := make([]goagent.EventKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}
