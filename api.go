package goagent

import (
	"context"
	"encoding/json"
)

// Agent describes the model, tools, instructions, and policy used for a run.
type Agent struct {
	Instructions string
	Model        Model
	Tools        []Tool
	Policy       Policy
}

// Runner executes an agent run and emits structured runtime events.
type Runner interface {
	Run(context.Context, RunRequest) (RunResult, error)
	Stream(context.Context, RunRequest) (<-chan Event, error)
}

// RunRequest is the host application's request to execute one agent run.
type RunRequest struct {
	Input   string
	Session Session
}

// RunResult is the final observable outcome of an agent run.
type RunResult struct {
	Text       string
	StopReason StopReason
	Session    Session
	Events     []Event
}

// Model is the provider-facing contract for producing the next model turn.
type Model interface {
	Turn(context.Context, TurnRequest) (TurnResult, error)
}

// TurnRequest is the runtime's provider-neutral request for one model turn.
type TurnRequest struct {
	Instructions string
	Messages     []Message
	Tools        []ToolSpec
	Session      Session
}

// TurnResult is the provider-neutral result of one model turn.
type TurnResult struct {
	Message    Message
	ToolCalls  []ToolCall
	StopReason StopReason
}

// Message is one transcript entry visible to the model or session store.
type Message struct {
	Role       Role
	Content    string
	Name       string
	ToolCallID string
}

// Role identifies the source of a transcript message.
type Role string

const (
	// RoleSystem identifies host-provided instructions.
	RoleSystem Role = "system"
	// RoleUser identifies user input.
	RoleUser Role = "user"
	// RoleAssistant identifies model output.
	RoleAssistant Role = "assistant"
	// RoleTool identifies tool output returned to the model.
	RoleTool Role = "tool"
)

// Tool is an executable Go capability available to the agent runtime.
type Tool interface {
	Name() string
	Description() string
	Schema() ToolSchema
	Call(context.Context, ToolCall) (ToolResult, error)
}

// ToolSpec is the model-visible description of a tool.
type ToolSpec struct {
	Name        string
	Description string
	Schema      ToolSchema
}

// ToolSchema describes accepted tool input without tying the core to one schema generator.
type ToolSchema map[string]any

// ToolCall is a model request to execute a named tool with JSON input.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult is the structured result of a tool call.
type ToolResult struct {
	CallID  string
	Name    string
	Content string
}

// Session carries conversation state and host-owned runtime metadata across runs.
type Session struct {
	ID       string
	Messages []Message
	Values   map[string]any
}

// Event is a structured record emitted while a run proceeds.
type Event struct {
	Kind       EventKind
	RunID      string
	TurnID     string
	ToolCallID string
	Text       string
	Message    Message
	ToolCall   ToolCall
	ToolResult ToolResult
	StopReason StopReason
	Err        error
}

// EventKind identifies the kind of runtime event.
type EventKind string

const (
	// EventTextDelta reports incremental assistant text.
	EventTextDelta EventKind = "text_delta"
	// EventToolCall reports that the model requested a tool.
	EventToolCall EventKind = "tool_call"
	// EventToolResult reports the result returned by a tool.
	EventToolResult EventKind = "tool_result"
	// EventPolicyDecision reports the policy outcome for a runtime decision.
	EventPolicyDecision EventKind = "policy_decision"
	// EventError reports a model, tool, runtime, or policy error.
	EventError EventKind = "error"
	// EventStop reports the reason a run stopped.
	EventStop EventKind = "stop"
)

// StopReason explains why the runtime stopped a run.
type StopReason string

const (
	// StopComplete means the model completed without requesting more work.
	StopComplete StopReason = "complete"
	// StopToolError means tool execution failed.
	StopToolError StopReason = "tool_error"
	// StopModelError means the model turn failed.
	StopModelError StopReason = "model_error"
	// StopPolicyDenied means host policy denied a requested action.
	StopPolicyDenied StopReason = "policy_denied"
	// StopStepLimit means the run reached its configured step limit.
	StopStepLimit StopReason = "step_limit"
	// StopCanceled means the context canceled before completion.
	StopCanceled StopReason = "canceled"
)

// Policy decides whether the runtime may perform a host-owned action.
type Policy interface {
	Decide(context.Context, Decision) (PolicyDecision, error)
}

// PolicyFunc adapts a function to the Policy interface.
type PolicyFunc func(context.Context, Decision) (PolicyDecision, error)

// Decide calls f(ctx, decision).
func (f PolicyFunc) Decide(ctx context.Context, decision Decision) (PolicyDecision, error) {
	return f(ctx, decision)
}

// Decision describes an action that policy can allow, deny, or constrain.
type Decision struct {
	ToolCall ToolCall
	Tool     ToolSpec
	Session  Session
}

// PolicyDecision is the host policy's answer for a runtime decision.
type PolicyDecision struct {
	Allowed bool
	Reason  string
}
