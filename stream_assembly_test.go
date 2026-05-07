package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestAssembleEventsReconstructsCanonicalRun(t *testing.T) {
	events := canonicalAssemblyEvents()

	assembled, err := goagent.AssembleEvents(events)
	if err != nil {
		t.Fatal(err)
	}

	if assembled.Text != "Use the weather tool." || assembled.StopReason != goagent.StopComplete {
		t.Fatalf("assembled result = %+v", assembled)
	}
	if len(assembled.Messages) != 2 {
		t.Fatalf("messages = %+v, want assistant and tool result", assembled.Messages)
	}
	assistant := assembled.Messages[0]
	if assistant.Role != goagent.RoleAssistant || assistant.Content != assembled.Text || len(assistant.Blocks) != 2 {
		t.Fatalf("assistant message = %+v", assistant)
	}
	if assistant.Blocks[0].Kind != goagent.BlockText || assistant.Blocks[0].Text != assembled.Text {
		t.Fatalf("text block = %+v", assistant.Blocks[0])
	}
	wantCall := goagent.ToolCall{ID: "call_weather", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}
	if !reflect.DeepEqual(assembled.ToolCalls, []goagent.ToolCall{wantCall}) {
		t.Fatalf("tool calls = %+v, want %+v", assembled.ToolCalls, []goagent.ToolCall{wantCall})
	}
	wantResult := goagent.ToolResult{CallID: "call_weather", Name: "weather", Content: "sunny", JSON: map[string]any{"temp_c": float64(27)}}
	if !reflect.DeepEqual(assembled.ToolResults, []goagent.ToolResult{wantResult}) {
		t.Fatalf("tool results = %+v, want %+v", assembled.ToolResults, []goagent.ToolResult{wantResult})
	}
	if assembled.Usage.InputTokens != 10 || assembled.Usage.OutputTokens != 7 || assembled.Usage.TotalTokens != 17 || assembled.Usage.RequestID != "req_123" {
		t.Fatalf("usage = %+v", assembled.Usage)
	}
}

func TestAssembleEventsReplayMatchesOrReportsDivergence(t *testing.T) {
	events := canonicalAssemblyEvents()
	original, err := goagent.AssembleEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := goagent.AssembleEvents(append([]goagent.Event(nil), events...))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, original) {
		t.Fatalf("replayed result differs:\ngot  %+v\nwant %+v", replayed, original)
	}

	contradictory := append([]goagent.Event(nil), events...)
	contradictory[9].Message.Content = "different"
	_, err1 := goagent.AssembleEvents(contradictory)
	_, err2 := goagent.AssembleEvents(contradictory)
	if !goagent.IsStreamDivergence(err1) || err1.Error() != err2.Error() {
		t.Fatalf("divergence not deterministic: err1=%v err2=%v", err1, err2)
	}
}

func TestAssembleEventsRejectsErrorBeforeResponseStartDeterministically(t *testing.T) {
	providerErr := errors.New("provider failed")
	events := []goagent.Event{{Kind: goagent.EventError, Err: providerErr}}

	_, err1 := goagent.AssembleEvents(events)
	_, err2 := goagent.AssembleEvents(events)
	if !goagent.IsStreamDivergence(err1) || err1.Error() != err2.Error() {
		t.Fatalf("divergence not deterministic: err1=%v err2=%v", err1, err2)
	}
}

func TestAssembleEventsRejectsTerminalErrorWithoutStopDeterministically(t *testing.T) {
	providerErr := errors.New("provider failed")
	events := []goagent.Event{{Kind: goagent.EventResponseStart}, {Kind: goagent.EventError, Err: providerErr}}

	_, err1 := goagent.AssembleEvents(events)
	_, err2 := goagent.AssembleEvents(events)
	if !goagent.IsStreamDivergence(err1) || err1.Error() != err2.Error() {
		t.Fatalf("divergence not deterministic: err1=%v err2=%v", err1, err2)
	}
}

func TestAssembleStreamTerminalBehaviorContract(t *testing.T) {
	setupErr := errors.New("setup failed")
	providerErr := errors.New("provider failed")
	toolErr := errors.New("tool failed")
	retryErr := errors.New("retry exhausted")

	tests := []struct {
		name      string
		events    []goagent.Event
		streamErr error
		wantErr   error
		wantStop  goagent.StopReason
	}{
		{name: "setup error before acceptance", streamErr: setupErr, wantErr: setupErr},
		{name: "provider error before accepted content", events: terminalEvents(providerErr, goagent.StopModelError), streamErr: providerErr, wantErr: providerErr, wantStop: goagent.StopModelError},
		{name: "accepted turn stream error", events: acceptedTextThenTerminal(providerErr, goagent.StopModelError), streamErr: providerErr, wantErr: providerErr, wantStop: goagent.StopModelError},
		{name: "tool execution error", events: finalizedToolCallThenTerminal(toolErr, goagent.StopToolError), streamErr: toolErr, wantErr: toolErr, wantStop: goagent.StopToolError},
		{name: "policy stop", events: []goagent.Event{{Kind: goagent.EventStop, StopReason: goagent.StopPolicyDenied}}, wantStop: goagent.StopPolicyDenied},
		{name: "context cancellation", events: terminalEvents(context.Canceled, goagent.StopCanceled), streamErr: context.Canceled, wantErr: context.Canceled, wantStop: goagent.StopCanceled},
		{name: "retry exhaustion", events: terminalEvents(retryErr, goagent.StopRetryExhausted), streamErr: retryErr, wantErr: retryErr, wantStop: goagent.StopRetryExhausted},
		{name: "step limit", events: []goagent.Event{{Kind: goagent.EventStop, StopReason: goagent.StopStepLimit}}, wantStop: goagent.StopStepLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assembled, err := goagent.AssembleStream(tt.events, tt.streamErr)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if assembled.StopReason != tt.wantStop {
				t.Fatalf("stop = %q, want %q", assembled.StopReason, tt.wantStop)
			}
			if tt.wantErr != nil && len(tt.events) > 0 && !errors.Is(assembled.Err, tt.wantErr) {
				t.Fatalf("assembled terminal err = %v, want %v", assembled.Err, tt.wantErr)
			}
		})
	}
}

func TestAssembleStreamRejectsMismatchedTerminalErrorDeterministically(t *testing.T) {
	terminalErr := errors.New("terminal provider error")
	streamErr := errors.New("returned provider error")
	events := terminalEvents(terminalErr, goagent.StopModelError)

	assembled1, err1 := goagent.AssembleStream(events, streamErr)
	assembled2, err2 := goagent.AssembleStream(events, streamErr)
	if !goagent.IsStreamDivergence(err1) || err1.Error() != err2.Error() {
		t.Fatalf("divergence not deterministic: err1=%v err2=%v", err1, err2)
	}
	if !errors.Is(assembled1.Err, terminalErr) || !reflect.DeepEqual(assembled1, assembled2) {
		t.Fatalf("assembled terminal results differ or lost terminal error:\ngot1 %+v\ngot2 %+v", assembled1, assembled2)
	}
}

func TestAssembleEventsRejectsContradictoryTerminalFacts(t *testing.T) {
	providerErr := errors.New("provider failed")
	tests := []struct {
		name   string
		events []goagent.Event
	}{
		{name: "final contradicts deltas", events: func() []goagent.Event {
			events := canonicalAssemblyEvents()
			events[9].Message.Blocks[0].Text = "other"
			return events
		}()},
		{name: "terminal error cannot stop complete", events: []goagent.Event{{Kind: goagent.EventResponseStart}, {Kind: goagent.EventError, Err: providerErr}, {Kind: goagent.EventStop, StopReason: goagent.StopComplete}}},
		{name: "terminal error before response_start", events: []goagent.Event{{Kind: goagent.EventError, Err: providerErr}}},
		{name: "terminal error without stop", events: []goagent.Event{{Kind: goagent.EventResponseStart}, {Kind: goagent.EventError, Err: providerErr}}},
		{name: "usage cannot follow stop", events: []goagent.Event{{Kind: goagent.EventStop, StopReason: goagent.StopStepLimit}, {Kind: goagent.EventUsage, Usage: goagent.Usage{InputTokens: 1}}}},
		{name: "duplicate terminal stop", events: []goagent.Event{{Kind: goagent.EventStop, StopReason: goagent.StopStepLimit}, {Kind: goagent.EventStop, StopReason: goagent.StopStepLimit}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := goagent.AssembleEvents(tt.events)
			if !goagent.IsStreamDivergence(err) {
				t.Fatalf("error = %v, want stream divergence", err)
			}
		})
	}
}

func canonicalAssemblyEvents() []goagent.Event {
	call := goagent.ToolCall{ID: "call_weather", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}
	result := goagent.ToolResult{CallID: call.ID, Name: call.Name, Content: "sunny", JSON: map[string]any{"temp_c": float64(27)}}
	assistant := goagent.Message{
		Role:    goagent.RoleAssistant,
		Content: "Use the weather tool.",
		Blocks: []goagent.Block{
			{ID: "block_text", Kind: goagent.BlockText, Text: "Use the weather tool."},
			{ID: "block_tool", Kind: goagent.BlockToolCall, ToolCall: call},
		},
		ToolCalls: []goagent.ToolCall{call},
	}
	toolMessage := goagent.Message{Role: goagent.RoleTool, ToolCallID: call.ID, Content: result.Content, Blocks: []goagent.Block{{ID: "block_result", Kind: goagent.BlockToolResult, ToolResult: result}}}

	return []goagent.Event{
		{Sequence: 1, Kind: goagent.EventResponseStart, MessageID: "msg_1", TurnID: "turn-1"},
		{Sequence: 2, Kind: goagent.EventContentBlockStart, MessageID: "msg_1", BlockID: "block_text", BlockKind: goagent.BlockText},
		{Sequence: 3, Kind: goagent.EventTextDelta, BlockID: "block_text", Text: "Use the "},
		{Sequence: 4, Kind: goagent.EventTextDelta, BlockID: "block_text", Text: "weather tool."},
		{Sequence: 5, Kind: goagent.EventContentBlockEnd, BlockID: "block_text", BlockKind: goagent.BlockText, Text: "Use the weather tool."},
		{Sequence: 6, Kind: goagent.EventContentBlockStart, MessageID: "msg_1", BlockID: "block_tool", BlockKind: goagent.BlockToolCall, ToolCallID: call.ID},
		{Sequence: 7, Kind: goagent.EventToolCallDelta, BlockID: "block_tool", ToolCallID: call.ID, ToolCallDelta: goagent.ToolCallDelta{NameDelta: "wea", ArgumentsDelta: `{"city"`}},
		{Sequence: 8, Kind: goagent.EventToolCallDelta, BlockID: "block_tool", ToolCallID: call.ID, ToolCallDelta: goagent.ToolCallDelta{NameDelta: "ther", ArgumentsDelta: `:"Austin"}`}},
		{Sequence: 9, Kind: goagent.EventContentBlockEnd, BlockID: "block_tool", BlockKind: goagent.BlockToolCall, ToolCallID: call.ID, ToolCall: call},
		{Sequence: 10, Kind: goagent.EventMessageFinal, MessageID: "msg_1", Message: assistant},
		{Sequence: 11, Kind: goagent.EventToolCallReady, ToolCallID: call.ID, ToolCall: call},
		{Sequence: 12, Kind: goagent.EventToolResult, ToolCallID: call.ID, BlockID: "block_result", ToolResult: result, Message: toolMessage},
		{Sequence: 13, Kind: goagent.EventUsage, Usage: goagent.Usage{InputTokens: 10, OutputTokens: 7, TotalTokens: 17, CachedInputTokens: 2, RequestID: "req_123", Provider: "test-provider", Model: "test-model", Meta: map[string]any{"tier": "test"}}},
		{Sequence: 14, Kind: goagent.EventStop, StopReason: goagent.StopComplete},
	}
}

func terminalEvents(err error, stop goagent.StopReason) []goagent.Event {
	return []goagent.Event{{Kind: goagent.EventResponseStart}, {Kind: goagent.EventError, Err: err}, {Kind: goagent.EventUsage, Usage: goagent.Usage{InputTokens: 1}}, {Kind: goagent.EventStop, StopReason: stop}}
}

func acceptedTextThenTerminal(err error, stop goagent.StopReason) []goagent.Event {
	return []goagent.Event{
		{Kind: goagent.EventResponseStart},
		{Kind: goagent.EventContentBlockStart, BlockID: "block_text", BlockKind: goagent.BlockText},
		{Kind: goagent.EventTextDelta, BlockID: "block_text", Text: "partial"},
		{Kind: goagent.EventError, Err: err},
		{Kind: goagent.EventStop, StopReason: stop},
	}
}

func finalizedToolCallThenTerminal(err error, stop goagent.StopReason) []goagent.Event {
	call := goagent.ToolCall{ID: "call_weather", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}
	return []goagent.Event{
		{Kind: goagent.EventResponseStart},
		{Kind: goagent.EventContentBlockStart, BlockID: "block_tool", BlockKind: goagent.BlockToolCall, ToolCallID: call.ID},
		{Kind: goagent.EventToolCallDelta, BlockID: "block_tool", ToolCallID: call.ID, ToolCallDelta: goagent.ToolCallDelta{NameDelta: call.Name, ArgumentsDelta: string(call.Input)}},
		{Kind: goagent.EventContentBlockEnd, BlockID: "block_tool", BlockKind: goagent.BlockToolCall, ToolCallID: call.ID, ToolCall: call},
		{Kind: goagent.EventMessageFinal, Message: goagent.Message{Role: goagent.RoleAssistant, Blocks: []goagent.Block{{ID: "block_tool", Kind: goagent.BlockToolCall, ToolCall: call}}, ToolCalls: []goagent.ToolCall{call}}},
		{Kind: goagent.EventToolCallReady, ToolCallID: call.ID, ToolCall: call},
		{Kind: goagent.EventError, Err: err},
		{Kind: goagent.EventStop, StopReason: stop},
	}
}
