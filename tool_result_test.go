package goagent_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestValidateToolResultRejectsNonMarshalableJSON(t *testing.T) {
	ch := make(chan int)
	err := goagent.ValidateToolResult(goagent.ToolResult{JSON: ch})
	if err == nil {
		t.Fatal("expected error for channel in JSON")
	}
	err = goagent.ValidateToolResult(goagent.ToolResult{Opaque: map[string]any{"x": ch}})
	if err == nil {
		t.Fatal("expected error for channel in Opaque")
	}
}

func TestValidateToolResultAcceptsMetadataAndOpaque(t *testing.T) {
	r := goagent.ToolResult{
		Content:  "hi",
		JSON:     map[string]any{"a": []any{float64(1), float64(2)}},
		Metadata: map[string]string{"tier": "pro"},
		Opaque:   map[string]any{"exitCode": float64(7), "shell": "bash"},
	}
	if err := goagent.ValidateToolResult(r); err != nil {
		t.Fatal(err)
	}
}

type richMetadataTool struct{}

func (richMetadataTool) Name() string { return "richmeta" }

func (richMetadataTool) Description() string { return "rich metadata tool" }

func (richMetadataTool) Schema() goagent.ToolSchema {
	return goagent.ToolSchema{"type": "object"}
}

func (richMetadataTool) Call(_ context.Context, call goagent.ToolCall) (goagent.ToolResult, error) {
	return goagent.ToolResult{
		CallID:          call.ID,
		Name:            "richmeta",
		Content:         "done",
		JSON:            map[string]any{"ok": true},
		Metadata:        map[string]string{"unit": "test"},
		Truncated:       true,
		OriginalBytes:   4096,
		Compressed:      true,
		CompressionKind: "gzip",
		SourceRef:       "s3://bucket/key",
		Opaque:          map[string]any{"exitCode": float64(0)},
	}, nil
}

func TestRunnerEmitsRichToolResultFields(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "c1", Name: "richmeta", Input: json.RawMessage(`{}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "OK"}, StopReason: goagent.StopComplete},
	}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{richMetadataTool{}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "run"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopComplete {
		t.Fatalf("stop = %q", result.StopReason)
	}
	var tr *goagent.ToolResult
	for _, e := range result.Events {
		if e.Kind == goagent.EventToolResult {
			tr = &e.ToolResult
			break
		}
	}
	if tr == nil {
		t.Fatal("missing tool result event")
	}
	want := goagent.ToolResult{
		CallID: "c1", Name: "richmeta", Content: "done",
		JSON:      map[string]any{"ok": true},
		Metadata:  map[string]string{"unit": "test"},
		Truncated: true, OriginalBytes: 4096,
		Compressed: true, CompressionKind: "gzip",
		SourceRef: "s3://bucket/key",
		Opaque:    map[string]any{"exitCode": float64(0)},
	}
	if !reflect.DeepEqual(*tr, want) {
		t.Fatalf("tool result = %+v, want %+v", *tr, want)
	}
}

func TestMarshalEventsRoundTripRichToolResult(t *testing.T) {
	tr := goagent.ToolResult{
		CallID: "x", Name: "t", Content: "c",
		JSON:      map[string]any{"n": float64(1)},
		Metadata:  map[string]string{"k": "v"},
		Truncated: true, OriginalBytes: 100,
		Opaque: map[string]any{"host": "z"},
	}
	events := []goagent.Event{
		{Sequence: 1, Kind: goagent.EventToolResult, RunID: "r1", ToolCallID: "x", ToolResult: tr},
	}
	data, err := goagent.MarshalEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	got, err := goagent.UnmarshalEvents(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].ToolResult, tr) {
		t.Fatalf("got %#v", got)
	}
	_, err = json.Marshal(got[0].ToolResult)
	if err != nil {
		t.Fatal(err)
	}
}
