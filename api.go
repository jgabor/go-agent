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

// RunLimits bounds resource consumption for a single run. Zero values mean no
// explicit limit for that field.
type RunLimits struct {
	// MaxSteps limits model turns for this run. When zero, RunRequest.MaxSteps
	// applies, then the runner default.
	MaxSteps int
	// MaxToolCalls limits how many tool executions complete in this run. When
	// zero, there is no tool-call count limit.
	MaxToolCalls int
	// MaxDuration bounds wall-clock time for the run via context deadline. When
	// zero, no duration limit is applied by the runtime.
	MaxDuration time.Duration
	// MaxToolOutputBytes bounds cumulative encoded tool result payload for this
	// run (approximated as json.Marshal of each ToolResult after validation).
	// When zero, there is no aggregate output limit.
	MaxToolOutputBytes int64
}

// RunRequest is the host application's request to execute one agent run.
type RunRequest struct {
	Input string
	// SessionID resumes a stored session by stable host-provided identifier.
	SessionID string
	Session   Session
	// Options carries provider-neutral model-turn controls for every model call in the run.
	Options TurnOptions
	// MaxSteps limits model turns in one run. Zero means the runtime default applies
	// unless Limits.MaxSteps is set.
	MaxSteps int
	// Instructions overrides Agent.Instructions for this run when non-empty.
	Instructions string
	// ToolNames selects which Agent-registered tools are visible for this run.
	// nil uses all Agent tools; an empty non-nil slice exposes no tools; each
	// listed name must exist on the Agent or Run/Stream returns an error before
	// the run starts.
	ToolNames []string
	// Limits applies optional run-level bounds. Zero-valued fields are ignored.
	Limits RunLimits
	// RunID overrides the auto-generated run identifier on emitted events when
	// non-empty after trimming whitespace.
	RunID string
	// ParentRunID is copied onto every emitted event for host-side lineage.
	ParentRunID string
	// TaskID is copied onto every emitted event for host-side task correlation.
	TaskID string
	// Metadata is shallow-copied onto every emitted event as opaque host labels.
	Metadata map[string]string
}

// RunResult is the final observable outcome of an agent run.
type RunResult struct {
	Text       string
	StopReason StopReason
	Usage      Usage
	Session    Session
	Events     []Event
}

// Model is the provider-facing contract for streaming the next model turn.
type Model interface {
	Stream(context.Context, TurnRequest, func(Event)) error
}

// TurnRequest is the runtime's provider-neutral request for one model turn.
type TurnRequest struct {
	Instructions string
	Messages     []Message
	Tools        []ToolSpec
	Session      Session
	Options      TurnOptions
}

// TurnOptions carries provider-neutral controls for one model turn.
type TurnOptions struct {
	MaxOutputTokens int
	Temperature     *float64
	StopSequences   []string
}

// SimpleModel is the ergonomic non-streaming model shape for tests and local models.
type SimpleModel interface {
	Turn(context.Context, TurnRequest) (TurnResult, error)
}

// SimpleModelFunc adapts a function to SimpleModel.
type SimpleModelFunc func(context.Context, TurnRequest) (TurnResult, error)

// Turn calls f(ctx, request).
func (f SimpleModelFunc) Turn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	return f(ctx, request)
}

// ModelFromSimple adapts a non-streaming SimpleModel to the streaming Model contract.
func ModelFromSimple(model SimpleModel) Model {
	return simpleModelAdapter{model: model}
}

// TurnResult is the provider-neutral final result used by SimpleModel adapters.
type TurnResult struct {
	Message     Message
	ToolCalls   []ToolCall
	StopReason  StopReason
	Usage       Usage
	Diagnostics ProviderDiagnostics
}

// Message is one transcript entry visible to the model or session store.
type Message struct {
	Role       Role
	Content    string
	Blocks     []Block
	Name       string
	ToolCallID string
	ToolCalls  []ToolCall
}

// Block is one ordered unit of transcript content.
type Block struct {
	ID         string
	Kind       BlockKind
	Text       string
	ToolCall   ToolCall
	ToolResult ToolResult
}

// BlockKind identifies the content carried by a transcript block.
type BlockKind string

const (
	// BlockText carries literal transcript text.
	BlockText BlockKind = "text"
	// BlockToolCall carries an assistant request to execute a tool.
	BlockToolCall BlockKind = "tool_call"
	// BlockToolResult carries a result returned by a tool.
	BlockToolResult BlockKind = "tool_result"
)

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
	// MaxProgressEvents caps EventToolProgress emissions per streaming invocation.
	// Zero means the runner default (1024).
	MaxProgressEvents int
	// MaxProgressBytes caps cumulative progress payload (text plus JSON encoding)
	// per streaming invocation. Zero means the runner default (512 KiB).
	MaxProgressBytes int64
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
	// JSON is structured model-facing data (object, array, or leaf) when the
	// tool returns more than plain text. It must be JSON-serializable; use
	// ValidateToolResult before returning from custom tools if unsure.
	JSON any
	// Metadata carries host-owned string key/value facts for sinks, policy, and
	// replay. Values are opaque to the core runtime beyond JSON safety.
	Metadata map[string]string
	// Truncated reports whether Content or JSON was truncated from a larger payload.
	Truncated bool
	// OriginalBytes is the pre-truncation byte length when Truncated is true.
	OriginalBytes int64
	// Compressed reports whether the payload was stored or transferred in compressed form.
	Compressed bool
	// CompressionKind names the compression when Compressed is true (e.g. "gzip").
	CompressionKind string
	// SourceRef is an optional citation, blob id, or URI string for the result source.
	SourceRef string
	// Opaque carries JSON-serializable host-only facts (for example subprocess
	// exit metadata) without the core interpreting them. Must pass ValidateToolResult.
	Opaque map[string]any
}

// ToolCallDelta carries one incremental tool-call fragment.
type ToolCallDelta struct {
	Index          int
	NameDelta      string
	ArgumentsDelta string
}

// Usage carries closed runtime/provider accounting facts.
type Usage struct {
	InputTokens       int
	OutputTokens      int
	TotalTokens       int
	CachedInputTokens int
	CacheWriteTokens  int
	RequestID         string
	Provider          string
	Model             string
}

// ProviderDiagnostics carries bounded non-secret provider metadata.
type ProviderDiagnostics struct {
	Provider      string
	Package       string
	RequestID     string
	HTTPStatus    int
	ErrorType     string
	ErrorCode     string
	RawStopReason string
	Excerpt       string
}

// ProviderError reports a provider failure with bounded diagnostics.
type ProviderError struct {
	Message     string
	Diagnostics ProviderDiagnostics
	Err         error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
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
	MessageID      string
	BlockID        string
	BlockKind      BlockKind
	ToolCallID     string
	Text           string
	Message        Message
	Tool           ToolSpec
	ToolCall       ToolCall
	ToolCallDelta  ToolCallDelta
	ToolResult     ToolResult
	ToolProgress   ToolProgress
	Usage          Usage
	Diagnostics    ProviderDiagnostics
	Decision       Decision
	PolicyDecision PolicyDecision
	Retry          RetryEvent
	StopReason     StopReason
	Err            error
	// ParentRunID links this event stream to a parent run when the host set it
	// on RunRequest.
	ParentRunID string
	// TaskID carries the host task identifier from RunRequest when set.
	TaskID string
	// Metadata carries opaque correlation labels from RunRequest when set.
	Metadata map[string]string
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
	// EventResponseStart reports that an assistant response stream was accepted.
	EventResponseStart EventKind = "response_start"
	// EventContentBlockStart reports that a streamed assistant content block started.
	EventContentBlockStart EventKind = "content_block_start"
	// EventTextDelta reports incremental assistant text.
	EventTextDelta EventKind = "text_delta"
	// EventToolCallDelta reports incremental tool-call name or argument bytes.
	EventToolCallDelta EventKind = "tool_call_delta"
	// EventContentBlockEnd reports that a streamed assistant content block ended.
	EventContentBlockEnd EventKind = "content_block_end"
	// EventMessageFinal reports the complete assistant message assembled for a turn.
	EventMessageFinal EventKind = "message_final"
	// EventToolCallReady reports that a finalized tool call is eligible for execution.
	EventToolCallReady EventKind = "tool_call_ready"
	// EventToolCall reports that the model requested a tool.
	EventToolCall EventKind = "tool_call"
	// EventToolResult reports the result returned by a tool.
	EventToolResult EventKind = "tool_result"
	// EventToolProgress reports incremental observational output from a streaming tool.
	EventToolProgress EventKind = "tool_progress"
	// EventUsage reports typed usage facts for a provider/runtime interaction.
	EventUsage EventKind = "usage"
	// EventPolicyPending reports that policy is about to be consulted for a
	// decision. It is emitted only when an explicit host Policy is configured,
	// immediately before Policy.Decide for run start, tool call, tool result, and
	// retry decisions. It is observational; the runtime still calls Decide
	// synchronously.
	EventPolicyPending EventKind = "policy_pending"
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
	// StopToolCallLimit means the run reached its configured tool-call limit.
	StopToolCallLimit StopReason = "tool_call_limit"
	// StopOutputLimit means cumulative tool output exceeded the configured limit.
	StopOutputLimit StopReason = "output_limit"
	// StopDurationLimit means the run's MaxDuration deadline was reached.
	StopDurationLimit StopReason = "duration_limit"
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
	// DecisionStop lets policy inspect the stable stop state before the runtime emits the terminal stop event.
	DecisionStop DecisionKind = "stop"
)

// Decision describes an action that policy can allow, deny, or constrain.
type Decision struct {
	Kind       DecisionKind
	Request    RunRequest
	ToolCall   ToolCall
	Tool       ToolSpec
	ToolResult ToolResult
	Message    Message
	Retry      RetryContext
	Session    Session
	StopReason StopReason
	Events     []Event
}

// PolicyDecision is the host policy's answer for a runtime decision.
type PolicyDecision struct {
	Allowed  bool
	Reason   string
	MaxSteps int
	ToolCall *ToolCall
	// ToolResult, when Allowed is false for a DecisionToolCall decision only,
	// supplies a synthetic tool outcome: the runtime skips tool execution,
	// appends a tool-role message with this result, emits EventToolResult, and
	// continues the run. When Allowed is false and ToolResult is nil, the run
	// stops with StopPolicyDenied (unchanged). When Allowed is true, ToolResult
	// is ignored.
	ToolResult *ToolResult
	Retry      RetryPolicy
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
	// RetryTargetTool identifies a retry-safe tool call retry.
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
