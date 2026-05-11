package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

type lineStreamTool struct{}

func (lineStreamTool) Name() string { return "linestream" }

func (lineStreamTool) Description() string { return "streaming lines" }

func (lineStreamTool) Schema() goagent.ToolSchema { return goagent.ToolSchema{"type": "object"} }

func (lineStreamTool) Metadata() goagent.ToolMetadata {
	return goagent.ToolMetadata{
		Safety:      goagent.ToolSafety{ReadOnly: true},
		Constraints: goagent.ToolConstraints{MaxProgressEvents: 20, MaxProgressBytes: 1 << 20},
	}
}

func (lineStreamTool) Call(context.Context, goagent.ToolCall) (goagent.ToolResult, error) {
	return goagent.ToolResult{}, errors.New("use CallStream")
}

func (lineStreamTool) CallStream(_ context.Context, call goagent.ToolCall, emit goagent.ToolProgressEmitter) (goagent.ToolResult, error) {
	for i := 0; i < 3; i++ {
		if err := emit.Emit(goagent.ToolProgress{Text: fmt.Sprintf("chunk-%d", i), JSON: map[string]any{"i": float64(i)}}); err != nil {
			return goagent.ToolResult{}, err
		}
	}
	return goagent.ToolResult{CallID: call.ID, Name: call.Name, Content: "done"}, nil
}

func TestStreamingToolEmitsProgressThenResult(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "c1", Name: "linestream", Input: json.RawMessage(`{}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "OK"}, StopReason: goagent.StopComplete},
	}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{lineStreamTool{}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopComplete {
		t.Fatalf("stop = %q", result.StopReason)
	}
	var kinds []goagent.EventKind
	var seqs []int64
	for _, e := range result.Events {
		if e.Kind == goagent.EventToolProgress {
			kinds = append(kinds, e.Kind)
			seqs = append(seqs, e.ToolProgress.Seq)
		}
	}
	if len(seqs) != 3 || seqs[0] != 1 || seqs[1] != 2 || seqs[2] != 3 {
		t.Fatalf("progress seqs = %v", seqs)
	}
	if len(kinds) != 3 {
		t.Fatalf("want 3 tool_progress, kinds=%v", kinds)
	}
	data, err := goagent.MarshalEvents(result.Events)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := goagent.UnmarshalEvents(data); err != nil {
		t.Fatal(err)
	}
}

type cancelStreamTool struct{ started *sync.WaitGroup }

func (cancelStreamTool) Name() string { return "cancelstream" }

func (cancelStreamTool) Description() string { return "d" }

func (cancelStreamTool) Schema() goagent.ToolSchema { return goagent.ToolSchema{"type": "object"} }

func (t *cancelStreamTool) Call(context.Context, goagent.ToolCall) (goagent.ToolResult, error) {
	return goagent.ToolResult{}, errors.New("use CallStream")
}

func (t *cancelStreamTool) CallStream(ctx context.Context, call goagent.ToolCall, emit goagent.ToolProgressEmitter) (goagent.ToolResult, error) {
	_ = emit.Emit(goagent.ToolProgress{Text: "starting"})
	if t.started != nil {
		t.started.Done()
	}
	<-ctx.Done()
	return goagent.ToolResult{}, ctx.Err()
}

func TestStreamingToolCancellationDuringProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "c1", Name: "cancelstream", Input: json.RawMessage(`{}`)}}},
	}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{&cancelStreamTool{started: &wg}}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var result goagent.RunResult
	var runErr error
	go func() {
		defer close(done)
		result, runErr = runner.Run(ctx, goagent.RunRequest{Input: "x"})
	}()
	wg.Wait()
	cancel()
	<-done
	if runErr != nil {
		t.Fatal(runErr)
	}
	if result.StopReason != goagent.StopCanceled {
		t.Fatalf("StopReason = %q", result.StopReason)
	}
	var sawProgress bool
	for _, e := range result.Events {
		if e.Kind == goagent.EventToolProgress {
			sawProgress = true
			break
		}
	}
	if !sawProgress {
		t.Fatal("expected at least one tool_progress before cancel")
	}
}

type boundedStreamTool struct{}

func (boundedStreamTool) Name() string { return "bounded" }

func (boundedStreamTool) Description() string { return "d" }

func (boundedStreamTool) Schema() goagent.ToolSchema { return goagent.ToolSchema{"type": "object"} }

func (boundedStreamTool) Metadata() goagent.ToolMetadata {
	return goagent.ToolMetadata{
		Safety:      goagent.ToolSafety{ReadOnly: true},
		Constraints: goagent.ToolConstraints{MaxProgressEvents: 2},
	}
}

func (boundedStreamTool) Call(context.Context, goagent.ToolCall) (goagent.ToolResult, error) {
	return goagent.ToolResult{}, errors.New("use CallStream")
}

func (boundedStreamTool) CallStream(_ context.Context, _ goagent.ToolCall, emit goagent.ToolProgressEmitter) (goagent.ToolResult, error) {
	_ = emit.Emit(goagent.ToolProgress{Text: "a"})
	_ = emit.Emit(goagent.ToolProgress{Text: "b"})
	return goagent.ToolResult{}, emit.Emit(goagent.ToolProgress{Text: "c"})
}

func TestStreamingToolProgressRespectsMaxEvents(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "c1", Name: "bounded", Input: json.RawMessage(`{}`)}}},
	}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{boundedStreamTool{}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopToolError {
		t.Fatalf("StopReason = %q", result.StopReason)
	}
}

type spoofProgressTool struct{}

func (spoofProgressTool) Name() string { return "spoofprogress" }

func (spoofProgressTool) Description() string { return "d" }

func (spoofProgressTool) Schema() goagent.ToolSchema { return goagent.ToolSchema{"type": "object"} }

func (spoofProgressTool) Call(context.Context, goagent.ToolCall) (goagent.ToolResult, error) {
	return goagent.ToolResult{}, errors.New("use CallStream")
}

func (spoofProgressTool) CallStream(_ context.Context, _ goagent.ToolCall, emit goagent.ToolProgressEmitter) (goagent.ToolResult, error) {
	return goagent.ToolResult{}, emit.Emit(goagent.ToolProgress{CallID: "other-call", Name: "other-tool", Text: "wrong"})
}

func TestStreamingToolProgressRejectsMismatchedIdentity(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "c1", Name: "spoofprogress", Input: json.RawMessage(`{}`)}}},
	}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{spoofProgressTool{}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopToolError {
		t.Fatalf("StopReason = %q", result.StopReason)
	}
	for _, event := range result.Events {
		if event.Kind == goagent.EventToolProgress {
			t.Fatalf("mismatched progress should not be emitted: %+v", event)
		}
	}
}

func TestValidateToolProgressRejectsBadJSON(t *testing.T) {
	ch := make(chan int)
	err := goagent.ValidateToolProgress(goagent.ToolProgress{JSON: ch})
	if err == nil {
		t.Fatal("expected error")
	}
}
