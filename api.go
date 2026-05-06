package goagent

import (
	"context"
	"encoding/json"
	"time"
)

// Agent describes the model, tools, instructions, and policy used for a run.
type Agent struct {
	Instructions string
	Model        Model
	Tools        []Tool
	Policy       Policy
	SessionStore SessionStore
	EventSinks   []EventSink
	// Retry enables bounded retry for model, runtime-owned, and retry-safe tool failures. Zero means disabled.
	Retry RetryPolicy
}

// Runner executes an agent run and emits structured runtime events.
type Runner interface {
	Run(context.Context, RunRequest) (RunResult, error)
	Stream(context.Context, RunRequest) (<-chan Event, error)
}

// RunRequest is the host application's request to execute one agent run.
type RunRequest struct {
	Input string
	// SessionID resumes a stored session by stable host-provided identifier.
	SessionID string
	Session   Session
	// MaxSteps limits model turns in one run. Zero means the runtime default applies.
	MaxSteps int
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
	ToolCalls  []ToolCall
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

// ToolDefinition is the advanced authoring path for tools that need explicit
// runtime metadata beyond the low-friction NewTool helper.
type ToolDefinition struct {
	Name        string
	Description string
	Schema      ToolSchema
	Function    any
	Safety      ToolSafety
	Constraints ToolConstraints
}

// ToolSafety carries policy-visible information about side effects and retry safety.
// Retryable must be true before the runtime will consider repeating a failed tool call.
type ToolSafety struct {
	ReadOnly  bool
	Retryable bool
}

// ToolConstraints carries runtime-relevant execution constraints for a tool.
type ToolConstraints struct {
	Timeout        time.Duration
	MaxOutputBytes int
}

// ToolMetadata is runtime metadata exposed by advanced tool implementations.
type ToolMetadata struct {
	Safety      ToolSafety
	Constraints ToolConstraints
}

// ToolSpec is the model-visible description of a tool.
type ToolSpec struct {
	Name        string
	Description string
	Schema      ToolSchema
	Safety      ToolSafety
	Constraints ToolConstraints
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

// SessionStore persists conversation state outside the runner.
type SessionStore interface {
	LoadSession(context.Context, string) (Session, error)
	SaveSession(context.Context, Session) error
}

// Event is a structured record emitted while a run proceeds.
type Event struct {
	// Sequence is a run-local, monotonically increasing event number.
	Sequence       int64
	Kind           EventKind
	RunID          string
	TurnID         string
	ToolCallID     string
	Text           string
	Message        Message
	Tool           ToolSpec
	ToolCall       ToolCall
	ToolResult     ToolResult
	Decision       Decision
	PolicyDecision PolicyDecision
	Retry          RetryEvent
	StopReason     StopReason
	Err            error
}

// EventSink observes runtime events without controlling run behavior.
type EventSink interface {
	HandleEvent(context.Context, Event)
}

// EventSinkFunc adapts a function to the EventSink interface.
type EventSinkFunc func(context.Context, Event)

// HandleEvent calls f(ctx, event).
func (f EventSinkFunc) HandleEvent(ctx context.Context, event Event) {
	f(ctx, event)
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
	// EventRetry reports retry consideration, attempts, skips, and terminal retry outcomes.
	EventRetry EventKind = "retry"
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
	// StopRetryExhausted means a retryable failure remained after the retry budget was exhausted.
	StopRetryExhausted StopReason = "retry_exhausted"
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

// DecisionKind identifies the runtime decision being presented to policy.
type DecisionKind string

const (
	// DecisionRunStart lets policy allow, deny, or constrain a run before model work starts.
	DecisionRunStart DecisionKind = "run_start"
	// DecisionToolCall lets policy allow, deny, or constrain a tool call before execution.
	DecisionToolCall DecisionKind = "tool_call"
	// DecisionToolResult lets policy validate tool output before it is returned to the model.
	DecisionToolResult DecisionKind = "tool_result"
	// DecisionRetry lets policy allow, deny, or constrain a runtime retry before it is attempted.
	DecisionRetry DecisionKind = "retry"
)

// Decision describes an action that policy can allow, deny, or constrain.
type Decision struct {
	Kind       DecisionKind
	Request    RunRequest
	ToolCall   ToolCall
	Tool       ToolSpec
	ToolResult ToolResult
	Retry      RetryContext
	Session    Session
}

// PolicyDecision is the host policy's answer for a runtime decision.
type PolicyDecision struct {
	Allowed  bool
	Reason   string
	MaxSteps int
	ToolCall *ToolCall
	Retry    RetryPolicy
}

// RetryContext is the typed policy-visible context for one considered retry.
//
// RetryContext is used with DecisionRetry. It intentionally models retry as a
// runtime decision, not as an unstructured lifecycle callback.
type RetryContext struct {
	Target      RetryTarget
	Reason      RetryReason
	Attempt     int
	MaxAttempts int
	Request     RunRequest
	Session     Session
	TurnID      string
	ToolCall    ToolCall
	Tool        ToolSpec
	Err         error
}

// RetryPolicy carries policy constraints for an allowed retry decision.
type RetryPolicy struct {
	// MaxAttempts constrains the total attempts for this retry target. Zero means
	// policy did not narrow the runtime's current retry budget.
	MaxAttempts int
	// Delay requests a minimum delay before the next attempt. Zero means no
	// policy-requested delay.
	Delay time.Duration
}

// RetryEvent is the structured event payload for EventRetry.
type RetryEvent struct {
	Target      RetryTarget
	Reason      RetryReason
	Attempt     int
	MaxAttempts int
	Outcome     RetryOutcome
	Delay       time.Duration
	StopReason  StopReason
}

// RetryTarget identifies what the runtime is considering or retrying.
type RetryTarget struct {
	Kind       RetryTargetKind
	TurnID     string
	ToolCallID string
	ToolName   string
}

// RetryTargetKind identifies the runtime operation a retry would repeat.
type RetryTargetKind string

const (
	// RetryTargetModel identifies a model turn retry.
	RetryTargetModel RetryTargetKind = "model"
	// RetryTargetRuntime identifies a runtime-owned operation retry.
	RetryTargetRuntime RetryTargetKind = "runtime"
	// RetryTargetTool is reserved until policy-visible tool safety metadata exists.
	RetryTargetTool RetryTargetKind = "tool"
)

// RetryReason is a structured explanation for why retry was considered.
type RetryReason string

const (
	// RetryReasonModelError means a model turn returned an error.
	RetryReasonModelError RetryReason = "model_error"
	// RetryReasonRuntimeError means a runtime-owned operation returned an error.
	RetryReasonRuntimeError RetryReason = "runtime_error"
	// RetryReasonToolError means a retryable tool call returned an error.
	RetryReasonToolError RetryReason = "tool_error"
	// RetryReasonToolRetryBlocked means tool retry was blocked by tool safety metadata.
	RetryReasonToolRetryBlocked RetryReason = "tool_retry_blocked"
)

// RetryOutcome explains what happened after retry was considered.
type RetryOutcome string

const (
	// RetryOutcomeConsidered means the runtime is about to ask policy about retry.
	RetryOutcomeConsidered RetryOutcome = "considered"
	// RetryOutcomeAttempted means a retry attempt started.
	RetryOutcomeAttempted RetryOutcome = "attempted"
	// RetryOutcomeSucceeded means a retry attempt recovered the operation.
	RetryOutcomeSucceeded RetryOutcome = "succeeded"
	// RetryOutcomeFailed means a retry attempt failed but more retry may still be considered.
	RetryOutcomeFailed RetryOutcome = "failed"
	// RetryOutcomeDisabled means retry was skipped because retry is disabled.
	RetryOutcomeDisabled RetryOutcome = "disabled"
	// RetryOutcomeDenied means policy denied retry.
	RetryOutcomeDenied RetryOutcome = "denied"
	// RetryOutcomeConstrained means policy allowed retry with narrower constraints.
	RetryOutcomeConstrained RetryOutcome = "constrained"
	// RetryOutcomeExhausted means no retry budget remains.
	RetryOutcomeExhausted RetryOutcome = "exhausted"
	// RetryOutcomeCanceled means cancellation prevented further retry.
	RetryOutcomeCanceled RetryOutcome = "canceled"
	// RetryOutcomeBlocked means runtime semantics block the retry target.
	RetryOutcomeBlocked RetryOutcome = "blocked"
)
