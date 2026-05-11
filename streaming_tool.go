package goagent

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	defaultMaxToolProgressEvents = 1024
	defaultMaxToolProgressBytes  = 512 * 1024
)

// ToolProgressKind classifies incremental streaming tool output.
type ToolProgressKind string

const (
	// ToolProgressOutput carries incremental text or log-style output.
	ToolProgressOutput ToolProgressKind = "output"
	// ToolProgressStatus carries a coarse status update (e.g. phase name).
	ToolProgressStatus ToolProgressKind = "status"
	// ToolProgressError carries a non-terminal diagnostic while the tool continues.
	ToolProgressError ToolProgressKind = "error"
)

// ToolProgress carries one observational chunk from a streaming tool invocation.
// JSON must be JSON-serializable. Seq is assigned by the runtime when emitting.
type ToolProgress struct {
	CallID string
	Name   string
	Kind   ToolProgressKind
	Text   string
	JSON   any
	Seq    int64
}

// ToolProgressEmitter receives incremental progress from a StreamingTool.
// Emit returns an error when the run context is done, progress bounds are
// exceeded, or JSON cannot be serialized; the tool should stop work and
// return that error from CallStream.
type ToolProgressEmitter interface {
	Emit(ToolProgress) error
}

// StreamingTool is an optional interface for tools that emit progress during
// execution. When implemented, the runner calls CallStream instead of Call.
// Tools that do not implement StreamingTool keep the single-shot Call path.
type StreamingTool interface {
	Tool
	CallStream(ctx context.Context, call ToolCall, emit ToolProgressEmitter) (ToolResult, error)
}

// ValidateToolProgress reports whether p is safe to emit (JSON marshals).
func ValidateToolProgress(p ToolProgress) error {
	if p.JSON == nil {
		return nil
	}
	if _, err := json.Marshal(p.JSON); err != nil {
		return fmt.Errorf("goagent: ToolProgress.JSON: %w", err)
	}
	return nil
}
