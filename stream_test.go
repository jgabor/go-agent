package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestStreamNormalCompletion(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{{
		Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Bring a jacket."},
		StopReason: goagent.StopComplete,
	}}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model})
	if err != nil {
		t.Fatal(err)
	}

	events, err := runner.Stream(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}

	var collected []goagent.Event
	for event := range events {
		collected = append(collected, event)
	}

	assertEventKinds(t, collected, textTurnEvents())
	assertOrderedCorrelatedEvents(t, collected)

	last := collected[len(collected)-1]
	if last.StopReason != goagent.StopComplete {
		t.Fatalf("stop reason = %q, want %q", last.StopReason, goagent.StopComplete)
	}
}

func TestStreamMultiTurnWithToolDispatch(t *testing.T) {
	tool, err := goagent.NewTool("weather", "Get weather.", func(ctx context.Context, city string) (string, error) {
		return "72F and clear in " + city, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Clear in Austin."}, StopReason: goagent.StopComplete},
	}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}})
	if err != nil {
		t.Fatal(err)
	}

	events, err := runner.Stream(context.Background(), goagent.RunRequest{Input: "Weather in Austin?"})
	if err != nil {
		t.Fatal(err)
	}

	var collected []goagent.Event
	for event := range events {
		collected = append(collected, event)
	}

	assertEventKinds(t, collected, toolThenTextEvents())
	assertOrderedCorrelatedEvents(t, collected)

	var toolCallEvent goagent.Event
	for _, e := range collected {
		if e.Kind == goagent.EventToolCall {
			toolCallEvent = e
			break
		}
	}
	if toolCallEvent.ToolCallID != "call-1" {
		t.Fatalf("tool call event ToolCallID = %q, want %q", toolCallEvent.ToolCallID, "call-1")
	}
	if toolCallEvent.ToolCall.Name != "weather" {
		t.Fatalf("tool call event ToolCall.Name = %q, want %q", toolCallEvent.ToolCall.Name, "weather")
	}

	var toolResultEvent goagent.Event
	for _, e := range collected {
		if e.Kind == goagent.EventToolResult {
			toolResultEvent = e
			break
		}
	}
	if toolResultEvent.ToolCallID != "call-1" {
		t.Fatalf("tool result event ToolCallID = %q, want %q", toolResultEvent.ToolCallID, "call-1")
	}
	if toolResultEvent.ToolResult.Content != "72F and clear in Austin" {
		t.Fatalf("tool result content = %q, want %q", toolResultEvent.ToolResult.Content, "72F and clear in Austin")
	}
}

func TestStreamStopsOnError(t *testing.T) {
	tests := []struct {
		name     string
		agent    goagent.Agent
		wantStop goagent.StopReason
	}{
		{
			name:     "model error",
			agent:    goagent.Agent{Model: &recordingModel{err: errors.New("model failed")}},
			wantStop: goagent.StopModelError,
		},
		{
			name: "tool error",
			agent: goagent.Agent{
				Model: &recordingModel{turns: []goagent.TurnResult{{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}}}},
				Tools: []goagent.Tool{namedTool{name: "weather", err: errors.New("tool failed")}},
			},
			wantStop: goagent.StopToolError,
		},
		{
			name: "policy denial",
			agent: goagent.Agent{
				Model:  &recordingModel{turns: []goagent.TurnResult{{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{}`)}}}}},
				Tools:  []goagent.Tool{namedTool{name: "weather"}},
				Policy: denyAllPolicy,
			},
			wantStop: goagent.StopPolicyDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner, err := goagent.NewRunner(tt.agent)
			if err != nil {
				t.Fatal(err)
			}

			events, err := runner.Stream(context.Background(), goagent.RunRequest{Input: "Go"})
			if err != nil {
				t.Fatal(err)
			}

			var collected []goagent.Event
			for event := range events {
				collected = append(collected, event)
			}

			if len(collected) == 0 {
				t.Fatal("no events emitted")
			}
			last := collected[len(collected)-1]
			if last.Kind != goagent.EventStop {
				t.Fatalf("last event kind = %q, want %q", last.Kind, goagent.EventStop)
			}
			if last.StopReason != tt.wantStop {
				t.Fatalf("stop reason = %q, want %q", last.StopReason, tt.wantStop)
			}
		})
	}
}

func TestStreamEventSequenceIsMonotonic(t *testing.T) {
	tool, err := goagent.NewTool("echo", "Echo input.", func(ctx context.Context, input string) (string, error) {
		return "echo: " + input, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "c1", Name: "echo", Input: json.RawMessage(`{"input":"hello"}`)}}},
		{ToolCalls: []goagent.ToolCall{{ID: "c2", Name: "echo", Input: json.RawMessage(`{"input":"world"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Done."}, StopReason: goagent.StopComplete},
	}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}})
	if err != nil {
		t.Fatal(err)
	}

	events, err := runner.Stream(context.Background(), goagent.RunRequest{Input: "Go"})
	if err != nil {
		t.Fatal(err)
	}

	var collected []goagent.Event
	for event := range events {
		collected = append(collected, event)
	}

	var runID string
	for i, event := range collected {
		if i > 0 && event.Sequence != collected[i-1].Sequence+1 {
			t.Fatalf("event %d sequence = %d, want %d", i, event.Sequence, collected[i-1].Sequence+1)
		}
		if i == 0 {
			runID = event.RunID
		} else if event.RunID != runID {
			t.Fatalf("event %d RunID = %q, want %q", i, event.RunID, runID)
		}
	}

	wantKinds := append(toolCallEvents(), goagent.EventToolCall, goagent.EventPolicyDecision, goagent.EventToolResult)
	wantKinds = append(wantKinds, toolCallEvents()...)
	wantKinds = append(wantKinds, goagent.EventToolCall, goagent.EventPolicyDecision, goagent.EventToolResult)
	wantKinds = append(wantKinds, textTurnEvents()...)
	assertEventKinds(t, collected, wantKinds)
}

func TestStreamCancellationEmitsStopEvent(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{{
		Message:    goagent.Message{Content: "unreached"},
		StopReason: goagent.StopComplete,
	}}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model})
	if err != nil {
		t.Fatal(err)
	}

	events, err := runner.Stream(canceledContext(), goagent.RunRequest{Input: "Go"})
	if err != nil {
		t.Fatal(err)
	}

	var collected []goagent.Event
	for event := range events {
		collected = append(collected, event)
	}

	if len(collected) == 0 {
		t.Fatal("no events emitted on cancellation")
	}
	last := collected[len(collected)-1]
	if last.Kind != goagent.EventStop || last.StopReason != goagent.StopCanceled {
		t.Fatalf("last event = %+v, want stop with canceled", last)
	}
}

func TestStreamAndRunProduceSameEventKinds(t *testing.T) {
	tool, err := goagent.NewTool("weather", "Get weather.", func(ctx context.Context, city string) (string, error) {
		return "clear in " + city, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Clear in Austin."}, StopReason: goagent.StopComplete},
	}}

	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}})
	if err != nil {
		t.Fatal(err)
	}

	runResult, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}

	streamModel := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Clear in Austin."}, StopReason: goagent.StopComplete},
	}}
	streamRunner, err := goagent.NewRunner(goagent.Agent{Model: streamModel, Tools: []goagent.Tool{tool}})
	if err != nil {
		t.Fatal(err)
	}

	events, err := streamRunner.Stream(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}

	var streamKinds []goagent.EventKind
	for event := range events {
		streamKinds = append(streamKinds, event.Kind)
	}

	runKinds := make([]goagent.EventKind, 0, len(runResult.Events))
	for _, event := range runResult.Events {
		runKinds = append(runKinds, event.Kind)
	}

	if !slices.Equal(runKinds, streamKinds) {
		t.Fatalf("Run event kinds = %v, Stream event kinds = %v", runKinds, streamKinds)
	}
}
