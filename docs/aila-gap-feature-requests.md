# Aila → go-agent Gap Feature Requests

Date: 2026-05-11

This document maps the gaps between what Aila (a planned terminal coding agent) needs from its runtime foundation and what go-agent currently provides. Each section is a self-contained feature request for the go-agent maintainers, written from the perspective of a downstream product embedding the runtime.

Scope: Library-level runtime primitives only. Aila-owned concerns (TUI, FSM, capability adapters, artifact resolver, built-in tool implementations, config loading, permission UI) are explicitly excluded — these are host product responsibilities per go-agent's stated library-first direction.

## Sources reviewed

| Source                     | Evidence used                                                                                                                                                                                                |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Aila README                | Product scope, CLI, provider list, autonomy levels, utility model, primitive tools, capabilities, TUI, session/state expectations: `~/git/aila/README.md:12-300`                                             |
| Aila workflow architecture | FSM, policy layer, capability contracts, artifact resolver, tool registry, event plane, utility boundary, permission boundary, package sketch, invariants: `~/git/aila/docs/workflow-architecture.md:19-658` |
| go-agent README            | Library scope, public primitives, host-owned concerns, providers, tools, sessions, policy hooks, roadmap, non-goals: `~/git/go-agent/README.md:3-478`                                                        |
| go-agent public API        | `Agent`, `Runner`, `RunRequest`, `Model`, `Tool`, `ToolResult`, `Session`, `Event`, `Policy`, `Retry` contracts: `~/git/go-agent/api.go:9-512`                                                               |
| go-agent runner            | Fixed runner instructions/tools, runtime loop, policy denial behavior, retry, session save, event emission: `~/git/go-agent/runner.go:14-822`                                                                |
| go-agent tool helpers      | Tool adapter signatures and generated schema limits; only `(string, error)` return supported: `~/git/go-agent/tool.go:17-292`, `~/git/go-agent/tool_execution.go:10-96`                                      |
| go-agent provider          | Current OpenAI-compatible Chat Completions adapter shape and limits: `~/git/go-agent/providers/openai/openai.go:1-655`                                                                                       |
| go-agent backlog           | Current deferred items for CLI, MCP adapter, and sub-agent coordination: `~/git/go-agent/TODO.md:45-57`                                                                                                      |

## Overall fit

`go-agent` is a strong fit for Aila's model/tool runtime core because it already provides a stream-first runner, tool dispatch, sessions, policy hooks, retries, event sinks, canonical events, and an OpenAI-compatible Chat Completions adapter.

The largest gaps are caused by Aila being a stateful terminal coding agent while `go-agent` currently exposes a runner whose instructions and tools are fixed at construction, whose policy denial ends a run, whose tools only produce final results, and whose events contain some in-memory-only values. Those are legitimate runtime-library seams, not Aila-specific UI features.

## Gap summary

| ID          | Feature request                                                    | Priority     | Why Aila needs it                                                                                                                                     | Current go-agent status                                                                                                              |
| ----------- | ------------------------------------------------------------------ | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| GA-AILA-001 | Run-scoped instructions and tool exposure                          | **Critical** | Aila's FSM requires state-specific prompts and state-limited tool visibility per phase.                                                               | `Agent.Instructions` and `Agent.Tools` are fixed at `NewRunner`; `RunRequest` has no override fields.                                |
| GA-AILA-002 | Recoverable policy denials for tool calls                          | **Critical** | Aila must return approval denial/defer results to the active capability instead of always terminating the run.                                        | Tool policy denial unconditionally returns `StopPolicyDenied`.                                                                       |
| GA-AILA-003 | Observable policy request lifecycle                                | **High**     | Aila's TUI needs to show an approval prompt while a decision is pending, not only after a policy decision exists.                                     | `EventPolicyDecision` is emitted after `Policy.Decide` returns; no pending-state event.                                              |
| GA-AILA-004 | Streaming tool progress events                                     | **High**     | Aila's `bash`, `fetch`, and edit-like tools need visible progress, partial output, cancellation, and no hidden long-running jobs.                     | `Tool.Call` returns one final `ToolResult`; tools cannot emit canonical runtime progress events.                                     |
| GA-AILA-005 | Richer tool result authoring and metadata                          | **High**     | Aila tool results need compressed summaries, exact source refs, truncation facts, and structured JSON. Helper tools cannot return structured results. | `ToolResult.JSON` exists, but helper-adapter tools only return `(string, error)` and `ToolResult` has no metadata/provenance fields. |
| GA-AILA-006 | Additional provider adapter packages                               | **High**     | Aila plans OpenAI Realtime, custom/OpenAI-compatible APIs, Anthropic, and several plan providers.                                                     | Only OpenAI-compatible Chat Completions SSE is implemented.                                                                          |
| GA-AILA-007 | Optional model capability metadata                                 | **Medium**   | Aila's `models` command and header need selected-model context window, tool support, reasoning support, and token accounting facts.                   | `Model` only streams; no optional descriptor/capability interface exists.                                                            |
| GA-AILA-008 | Replay-safe event serialization                                    | **Medium**   | Aila saves session history, runs, approvals, command output, reviews, undo data, and compacted context under `.aila/`.                                | Events include `Err error`; event persistence is host-owned and lacks a JSON-safe canonical representation.                          |
| GA-AILA-009 | Parent/child run event correlation for utility work and sub-agents | **Medium**   | Aila needs foreground model runs, utility model jobs, and orchestrated sub-agent runs to be multiplexed without losing lineage.                       | Each run has a local run ID; sub-agent coordination is deferred outside core.                                                        |
| GA-AILA-010 | Run-level limits beyond model steps                                | **Medium**   | Aila needs bounded autonomy for tool calls, wall-clock duration, output volume, and budget-sensitive execution.                                       | `RunRequest.MaxSteps`, context cancellation, and per-tool constraints exist, but no run-level limits envelope exists.                |

---

## GA-AILA-001: Run-scoped instructions and tool exposure

**Priority:** Critical
**go-agent files:** `api.go:10-37`, `runner.go:15-55,69-181`

### What Aila needs

Aila's FSM selects different system prompts and tool visibility per phase. When the FSM is in BUILD, the model must see build-oriented instructions and only build-appropriate tools. When in AUDIT, it needs audit instructions and audit tools. The capability adapter decides what tools and instructions the model gets per turn, without reconstructing the Agent or Runner.

The workflow architecture defines per-state tool exposure tables (`~/git/aila/docs/workflow-architecture.md:459-476`): "The model must see only tools allowed for the current state plus global cross-cutting utilities."

Aila needs to:

1. Override `Instructions` per run (not just per Agent construction).
2. Constrain the set of `Tools` presented to the model per run — a subset of the Agent's full tool registry, or a completely overridden list.
3. Optionally disable all tools for information-gathering turns.

### Current go-agent behavior

- `Agent.Instructions` is set once at construction (`api.go:11`), read by the runner (`runner.go:69`) into `r.instructions`, and passed to every `TurnRequest` as `r.instructions` (`runner.go:131`).
- `Agent.Tools` is frozen at `NewRunner` time: `buildToolRegistry` (`runner.go:771-789`) builds `r.tools` (map) and `r.toolSpecs` (slice), and `r.toolSpecs` is cloned into every `TurnRequest` (`runner.go:133`).
- `RunRequest` has no fields for per-run instruction or tool overrides (`api.go:28-37`).

### Proposed solution

Add per-run override fields to `RunRequest`. The runner should prefer a run-scoped override when present and fall back to the Agent-level value. Tool exposure should support either a subset filter or a full replacement list.

```go
type RunRequest struct {
	Input     string
	SessionID string
	Session   Session
	Options   TurnOptions
	MaxSteps  int

	// Run-scoped override for Agent.Instructions. When non-empty,
	// replaces the Agent-level instructions for this run only.
	Instructions string

	// Run-scoped override for Agent.Tools. When non-nil, replaces the
	// Agent-level tools for this run. An empty but non-nil slice means
	// the model sees no tools for this run.
	Tools []Tool
}
```

`runner.go` changes: In `run()`, resolve `instructions` and `toolSpecs` per run:

```go
func (r *runner) run(ctx context.Context, request RunRequest, emit func(Event)) (RunResult, error) {
	instructions := r.instructions
	if request.Instructions != "" {
		instructions = request.Instructions
	}
	toolSpecs := r.toolSpecs
	if request.Tools != nil {
		var tools map[string]registeredTool
		tools, toolSpecs, err = buildRunToolRegistry(r.tools, request.Tools)
		if err != nil {
			return RunResult{}, err
		}
		_ = tools // used by callTool lookup
	}
	// ... pass instructions and toolSpecs to each TurnRequest ...
}
```

`buildRunToolRegistry` resolves tool names from the request against the runner's full registry; unknown names produce a construction error returned from `Run`/`Stream`.

An alternative would be a `ToolProvider` or `RunConfig` interface if the maintainers prefer not to grow `RunRequest` directly.

### Acceptance criteria

1. `RunRequest.Instructions` overrides `Agent.Instructions` when non-empty; empty-string override has no effect.
2. `RunRequest.Tools` (non-nil) replaces the model-visible tools; nil keeps Agent-level tools.
3. `RunRequest.Tools` (non-nil, empty) results in zero tools in `TurnRequest.Tools`.
4. `RunRequest.Tools` containing a name not in the Agent registry returns a construction error.
5. `Agent.Instructions` and `Agent.Tools` are unchanged across runs — overrides do not leak between runs.
6. `Stream` and `Run` produce identical tool exposure for identical override requests.
7. Policy decisions and events include the effective tool specs used for the run.
8. The feature does not introduce a workflow DSL, plugin loader, marketplace, or state machine into `go-agent`.

---

## GA-AILA-002: Recoverable policy denials for tool calls

**Priority:** Critical
**go-agent files:** `api.go:364-414`, `runner.go:355-410`

### What Aila needs

When a tool call is denied by policy (e.g., autonomy level blocks a file write, or user rejects the approval prompt), Aila needs the denial returned as a structured tool result to the model. The model can then adapt — ask for clarification, try a different approach, or explain why the operation is needed. The run must not terminate.

Aila's architecture describes an approval flow where denial produces a result that goes back to the capability adapter and potentially to the model as a tool response (`~/git/aila/docs/workflow-architecture.md:562-574`). Aila's exit-signal contract differentiates `complete`, `flagged`, `stuck`, and `waiting` (`~/git/aila/docs/workflow-architecture.md:191-224`). Permission decisions include approve, deny, and defer paths (`~/git/aila/docs/workflow-architecture.md:558-576`).

### Current go-agent behavior

In `runner.go:372-375`, when `PolicyDecision.Allowed` is `false` for `DecisionToolCall`, the runner calls `r.finish(ctx, &state, turnID, StopPolicyDenied)` — the run terminates with `StopPolicyDenied`. The model never learns why the call was denied.

Similarly, `DecisionToolResult` denial (`runner.go:403-405`) also terminates the run.

### Proposed solution

Add a recoverable denial path to `PolicyDecision`. When policy sets `Allowed = false` AND provides a non-nil `ToolResult`, the runtime treats the denial as a synthetic tool result: the tool call is not executed, but a `role: tool` message with the denial result is appended to the session and returned to the model on the next turn. The run continues.

When `Allowed = false` and `ToolResult` is nil, the existing terminate-on-denial behavior is preserved (backward compatible).

```go
type PolicyDecision struct {
	Allowed  bool
	Reason   string
	MaxSteps int
	ToolCall *ToolCall
	Retry    RetryPolicy

	// ToolResult, when non-nil and Allowed is false, signals a recoverable
	// denial. The runtime synthesizes a tool result message from ToolResult
	// and continues the run so the model can adapt.
	ToolResult *ToolResult
}
```

`runner.go` changes for `callTool`:

```go
if !policyDecision.Allowed {
	if policyDecision.ToolResult != nil {
		result := *policyDecision.ToolResult
		result.CallID = call.ID
		result.Name = call.Name
		state.session.Messages = append(state.session.Messages, toolResultMessage(call, result))
		state.toolResult(turnID, spec, call, result, toolResultMessage(call, result))
		return nil
	}
	returnResult := r.finish(ctx, state, turnID, StopPolicyDenied)
	return &returnResult
}
```

### Acceptance criteria

1. `PolicyDecision.Allowed = false` with non-nil `ToolResult` → run continues; denial result appears as a tool result message in the session transcript.
2. `PolicyDecision.Allowed = false` with nil `ToolResult` → run terminates with `StopPolicyDenied` (unchanged behavior).
3. Recoverable denial emits the usual `EventPolicyDecision` and `EventToolResult` events.
4. The model sees the denial result on its next turn and can produce follow-up messages or tool calls.
5. Recoverable denial works identically for `DecisionToolCall` and `DecisionToolResult`.
6. Run-start and stop decisions are not forced into this recoverable tool-result path.

---

## GA-AILA-003: Observable policy request lifecycle

**Priority:** High
**go-agent files:** `api.go:364-414`, `runner.go:533-539`

### What Aila needs

Aila's TUI renders an approval prompt when a tool call requires user consent. The TUI must know: (a) a decision is pending, (b) what was decided, and (c) the result. Currently, `Policy.Decide` is synchronous — the TUI cannot observe the "pending" state because the policy blocks the run goroutine.

Aila's architecture describes explicit approval flow with pending, granted, and denied states (`~/git/aila/docs/workflow-architecture.md:531`). Aila must preserve user trust with no hidden edits, no hidden long-running jobs, and no silent state changes (`~/git/aila/docs/workflow-architecture.md:521-535`). Permission decisions must be tied to exact proposal data before mutation: operation kind, target path, target version, diff, command, working directory, expected effect, and approval identity (`~/git/aila/docs/workflow-architecture.md:558-576`).

### Current go-agent behavior

`r.decide()` calls `r.policy.Decide(ctx, cloneDecision(decision))` synchronously (`runner.go:533-539`). The run is blocked until the policy returns. An `EventPolicyDecision` is emitted only after the decision. There is no "awaiting policy" event.

### Proposed solution

Add event kinds for the approval lifecycle and emit them around policy decisions:

```go
const (
	EventPolicyPending  EventKind = "policy_pending"
	EventPolicyDecision EventKind = "policy_decision" // existing
)
```

Add metadata to `Decision` for tools that want to pass display hints:

```go
type Decision struct {
	// ... existing fields ...

	// Display carries optional hints for approval UI rendering.
	Display DecisionDisplay
}

type DecisionDisplay struct {
	Operation string // "read", "write", "execute", "fetch"
	Target    string // file path, URL, command
	Summary   string // one-line human-readable summary
	Diff      string // proposed diff for edits
}
```

In `runner.go`, emit `EventPolicyPending` before calling `Decide`, and `EventPolicyDecision` after:

```go
func (r *runner) decide(ctx context.Context, state *runState, turnID string, decision Decision) (PolicyDecision, error) {
	if r.policyExplicit {
		state.policyPending(turnID, decision)
	}
	policyDecision, err := r.policy.Decide(ctx, cloneDecision(decision))
	if r.policyExplicit || decision.Kind == DecisionToolCall || decision.Kind == DecisionRetry {
		state.policyDecision(turnID, decision, policyDecision)
	}
	return policyDecision, err
}
```

Key insight: `EventPolicyPending` is purely observational. The runtime does NOT pause between pending and decision — the policy is still called synchronously. But hosts with async approval UIs can observe pending events, pause the run via their policy implementation (which blocks until user input), and render the approval prompt using the event payload.

For hosts that want truly non-blocking policy evaluation, the `Policy.Decide` implementation can use channels or futures internally — `go-agent` does not need an async policy contract. The event lifecycle alone is sufficient for TUI observation.

### Acceptance criteria

1. `EventPolicyPending` is emitted immediately before `Policy.Decide` is called.
2. `EventPolicyPending` carries the full `Decision` payload (including run ID, turn ID, tool call ID, and sequence number) so sinks and the TUI have context.
3. `EventPolicyDecision` is emitted immediately after `Policy.Decide` returns (existing behavior).
4. The runtime does not pause between pending and decision — the two events are back-to-back for synchronous policies.
5. `EventPolicyPending` is suppressed for `allowAllPolicy` (nil policy) — no unnecessary noise.
6. The pending request can be correlated with the final policy decision event.
7. Cancellation while waiting for policy is visible as a stop/cancel event.
8. Existing non-interactive hosts can ignore the new event without changing behavior.
9. `Decision.Display` is nil-safe; tools/policies that do not set it produce no display data.

---

## GA-AILA-004: Streaming tool progress events

**Priority:** High
**go-agent files:** `api.go:141-211`, `tool.go`, `tool_execution.go:10-40`, `runner.go:355-410`

### What Aila needs

Aila's shell tool (`bash`) runs commands that may take seconds to minutes. The `fetch` tool downloads content over the network. The TUI must show partial output as it arrives, not just a single result after completion. The user must be able to cancel a long-running tool via context.

Aila's architecture describes "visible progress" as a TUI requirement (`~/git/aila/docs/workflow-architecture.md:63`) and its non-negotiables list "low latency without sacrificing a rich user experience." The event plane must not hide long-running background jobs (`~/git/aila/docs/workflow-architecture.md:521-535`). Aila's non-negotiables require no hidden long-running jobs and visible progress (`~/git/aila/docs/workflow-architecture.md:605-620`).

### Current go-agent behavior

`Tool.Call(ctx, ToolCall) (ToolResult, error)` is a single-call contract. The runner calls it, waits for the result, and emits exactly one `EventToolResult` (`runner.go:355-409`). Helper-executed tools run a function and return final string content (`tool_execution.go:14-40`). There is no mechanism for incremental progress.

### Proposed solution

Introduce an optional `StreamingTool` interface. A tool that implements it exposes a streaming call path. The runtime detects the interface and uses the streaming path when available, falling back to `Call` for tools that do not implement it.

```go
// ToolProgress carries incremental output from a long-running tool call.
type ToolProgress struct {
	CallID string
	Name   string
	Kind   ToolProgressKind
	Text   string
	JSON   any
	Seq    int64
}

type ToolProgressKind string

const (
	ToolProgressOutput ToolProgressKind = "output"
	ToolProgressStatus ToolProgressKind = "status"
	ToolProgressError  ToolProgressKind = "error"
)

// ToolProgressEmitter pushes progress events during a streaming tool call.
type ToolProgressEmitter interface {
	Emit(ToolProgress)
}

// StreamingTool is an optional interface for tools that emit progress during
// execution. Tools that do not implement this use the synchronous Call path.
type StreamingTool interface {
	Tool
	CallStream(context.Context, ToolCall, ToolProgressEmitter) (ToolResult, error)
}
```

Add an `EventToolProgress` event kind:

```go
const EventToolProgress EventKind = "tool_progress"
```

The runner detects `StreamingTool` in `callTool` and routes accordingly. Progress events are emitted via the standard event pipeline (emit, sinks, channel). Context cancellation during streaming produces a `StopCanceled` result.

### Acceptance criteria

1. Tools implementing `StreamingTool` use `CallStream`; others use `Call` (no behavior change for non-streaming tools).
2. Each `CallStream` invocation produces zero or more `EventToolProgress` events followed by exactly one `EventToolResult` (or `EventError`).
3. `ToolProgress.Seq` increments monotonically per call.
4. Context cancellation during `CallStream` propagates via `ctx.Err()`; the tool returns promptly and the runtime emits the appropriate stop/error events.
5. `Run`, `Stream`, and `EventSink` all receive `EventToolProgress` events.
6. Policy decisions (`DecisionToolCall`) fire before streaming begins, not after.
7. Progress events are observational and do not alter transcript assembly unless explicitly returned in the final `ToolResult`.
8. `go-agent` does not become a shell runner; host applications still implement concrete tools.

---

## GA-AILA-005: Richer tool result metadata and helper signatures

**Priority:** High
**go-agent files:** `api.go:198-204`, `tool.go:17-21`, `tool_execution.go:14-40`

### What Aila needs

Aila's built-in tools produce results that go beyond a flat content string:

- **Compression summaries**: Bash output may be thousands of lines; the tool summarizes and full output is available via source reference.
- **Truncation facts**: When output exceeds `MaxOutputBytes`, the result must indicate how much was truncated.
- **Source references**: Exact paths, line ranges, and versions that let the TUI link to locations.
- **Structured outputs**: Some tools produce structured JSON (e.g., `grep` returns match counts, file paths, line contexts).

The architecture states: "Tool results should include compressed summaries plus exact source references when correctness may depend on the original output" (`~/git/aila/docs/workflow-architecture.md:476`).

Aila also needs the low-friction tool helpers to return structured results, not only strings, so developers can preserve metadata without writing a custom `Tool` every time.

### Current go-agent behavior

`ToolResult` has two content fields (`api.go:198-204`):

```go
type ToolResult struct {
	CallID  string
	Name    string
	Content string
	JSON    any
}
```

There is no metadata map, no source reference struct, no truncation indicator, and no compression marker.

Helper tools (`NewTool`) only support `(string, error)` return (`tool.go:17-21`). Helper execution returns `ToolResult{Content: content}` and enforces max output bytes only on the string (`tool_execution.go:29-40`). A tool author who needs `JSON` or metadata must implement the `Tool` interface from scratch.

### Proposed solution

Expand `ToolResult` with a `Metadata` map and define standard metadata keys. Expand helper-supported function signatures to allow returning `ToolResult` or `any` (JSON). Keep `Content` as the primary text representation. Keep `JSON` for structured data.

```go
type ToolResult struct {
	CallID  string
	Name    string
	Content string
	JSON    any

	// Metadata carries structured facts about this result. Standard keys are
	// defined as exported constants. Custom keys are allowed.
	Metadata map[string]any
}
```

Standard metadata constants:

```go
const (
	ToolResultTruncated      = "truncated"       // bool — Content was truncated
	ToolResultTruncatedBytes = "truncated_bytes" // int — total pre-truncation size
	ToolResultCompressed     = "compressed"      // bool — Content is a summary
	ToolResultSourceFiles    = "source_files"    // []string — file paths referenced
	ToolResultSourceLines    = "source_lines"    // string — e.g. "10-25,42"
	ToolResultExitCode       = "exit_code"       // int — shell exit code
	ToolResultDuration       = "duration"        // string — wall-clock duration
)
```

Expanded helper function signatures:

```go
// Existing (preserved):
func(context.Context, string) (string, error)
func(context.Context, T) (string, error)

// New:
func(context.Context, T) (ToolResult, error)
func(context.Context, T) (any, error)
```

`ToolResult.Metadata` is included in `EventToolResult` payloads so sinks see it. It is NOT sent to the model in `TurnRequest` (the model sees `Content` and optionally `JSON` as structured blocks).

### Acceptance criteria

1. `ToolResult.Metadata` is nil-safe; tools that do not set it produce no metadata in events.
2. Standard metadata keys are documented in `api.go` as exported constants.
3. `EventToolResult.Metadata` is populated from `ToolResult.Metadata`.
4. `CloneToolResult` (internal) deep-copies the metadata map.
5. Metadata maps are JSON-serializable (values are `string`, `int`, `float64`, `bool`, `[]any`, `map[string]any`).
6. Existing tools (tests, openai provider) continue to work without metadata.
7. Existing `(string, error)` helpers continue to work unchanged.
8. Helper tools can return `ToolResult` directly, populating `Content`, `JSON`, and `Metadata`.
9. Structured JSON results survive events and transcript blocks.
10. Metadata can carry generic facts such as `truncated`, `summary_of`, `source_refs`, `artifact_refs`, `stdout_path`, or `stderr_path` without `go-agent` interpreting Aila-specific semantics.
11. Output-size enforcement is documented for both text and structured results.

---

## GA-AILA-006: Additional provider adapters beyond Chat Completions

**Priority:** High
**go-agent files:** `providers/`

### What Aila needs

Aila's README lists five plan providers and three API providers (`~/git/aila/README.md:134-150`):

- **API**: `custom` (OpenAI-compatible), `openai` (Realtime API), `opencode-zen`
- **Plans**: `codex` (OpenAI Codex), `copilot` (GitHub Copilot), `opencode-go`, `xiaomi-plan`, `zai-plan`

The existing `providers/openai` adapter covers Chat Completions but not the Realtime API. There are no adapters for Anthropic, Anthropic-compatible, or any plan/device-code-auth providers.

Model names may include reasoning suffixes, and `aila models` should be the source of truth for provider support (`~/git/aila/README.md:115-122`). Aila has a separate primary model and utility model (`~/git/aila/README.md:100-120,186-195`).

### Current go-agent behavior

The only provider adapter is `providers/openai.ChatModel`, which targets OpenAI-compatible Chat Completions SSE endpoints. It supports custom `BaseURL` for compatible backends (covers "custom" providers), but has no Realtime API (WebSocket), no Anthropic Messages API, and no device-code-authenticated provider paths.

The README lists Anthropic, Google Gemini, Azure OpenAI, Bedrock, Ollama, and local gateways as possible adapter targets, but only the OpenAI-compatible provider is marked as started (`~/git/go-agent/README.md:178-207,424-443`).

### Proposed solution

Add provider adapters incrementally, each in its own `providers/<name>/` package, all implementing `goagent.Model`:

1. **`providers/anthropic/`**: Messages API with streaming SSE. `ChatModel` struct with `Model`, `APIKey`, `BaseURL`, `HTTPClient`, `Options`. Maps `TurnRequest` to Anthropic Messages shape.
2. **`providers/openai/` extension**: Add `RealtimeModel` for the Realtime API (WebSocket-based). This is a separate type from `ChatModel` since the transport and protocol differ. Also add `ResponsesModel` if OpenAI Responses API can be adapted to the `Model.Stream` contract.
3. **`providers/openai/` enhancements**: Custom headers, non-bearer auth hooks, path customization, and provider-safe diagnostics for OpenAI-compatible gateways.

Example API shape:

```go
// providers/anthropic/anthropic.go
type ChatModel struct {
	Model      string
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	Options    ChatOptions
}

// providers/openai/realtime.go
type RealtimeModel struct {
	Model      string
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	Options    RealtimeOptions
}
```

### Acceptance criteria

1. Each new provider adapter implements `goagent.Model`.
2. Anthropic adapter maps stop reasons, tool calls, content blocks, usage, and error paths to canonical go-agent events with parity to the OpenAI adapter.
3. Provider packages emit the same canonical `goagent.Event` grammar as the existing Chat Completions adapter.
4. Providers handle cancellation correctly (context propagation, `StopCanceled`).
5. Each provider package has its own test file covering event stream contract and error paths.
6. Raw auth headers, tokens, and credentials are excluded from diagnostic excerpts (same standard as `providers/openai`).
7. Provider adapters compose with `ModelFromSimple` and event sinks without additional glue.
8. Provider packages do not perform product credential discovery, device-code login, token exchange, provider registry selection, model marketplace behavior, or Aila config loading.
9. Adapter options are typed where possible and bounded where pass-through is necessary.

---

## GA-AILA-007: Optional model capability metadata

**Priority:** Medium
**go-agent files:** `api.go:48-51`, `providers/openai/openai.go`

### What Aila needs

Aila displays model information in the TUI header and responds to the `/model` command. Users switch models and need to know:

- Context window size (for compaction decisions)
- Max output tokens
- Whether the model supports tool/function calling
- Whether it supports streaming
- Whether it supports reasoning effort levels
- Which reasoning efforts are available

Aila's README defines a model format of `<provider>/<model>[:reasoning]` and a `models` command (`~/git/aila/README.md:41-52,102-122`). The utility model needs context window size to make compaction decisions. The header shows primary model, utility model, context window, and autonomy level (`~/git/aila/README.md:250-259`).

### Current go-agent behavior

`Model` is a single-method interface (`api.go:48-51`):

```go
type Model interface {
	Stream(context.Context, TurnRequest, func(Event)) error
}
```

There is no way to ask a model what it supports. `providers/openai.ChatModel` has a `ChatOptions` struct with `ReasoningEffort` and `ResponseFormat`, but these are configuration, not introspection. There is no context-window metadata.

### Proposed solution

Add an optional `CapabilityModel` interface. Models that support introspection implement it. Consumers type-assert the `Model` to `CapabilityModel` to read capabilities.

```go
// ModelCapabilities describes a model's known limits and features.
type ModelCapabilities struct {
	Provider string
	Model    string

	ContextWindow  int
	MaxOutputTokens int

	SupportsTools          bool
	SupportsStreaming      bool
	SupportsReasoningEffort bool
	ReasoningEfforts        []string
}

// CapabilityModel is an optional interface for models that expose capability
// metadata. Consumers may type-assert a Model to CapabilityModel.
type CapabilityModel interface {
	Model
	Capabilities(context.Context) (ModelCapabilities, error)
}
```

`providers/openai.ChatModel` implements `CapabilityModel`. Known model caps are populated from a built-in table (e.g., GPT-4o: 128k context, 16384 max output). Unknown models return zero-valued caps without error — consumers treat unknown as "assume nothing."

### Acceptance criteria

1. `CapabilityModel` is a separate interface; models that do not implement it remain valid.
2. `providers/openai.ChatModel` implements `CapabilityModel` for known models; unknown model IDs return a zero-valued `ModelCapabilities` with no error.
3. `ModelCapabilities.ContextWindow` is usable for host-side compaction decisions.
4. `Capabilities(ctx)` respects context cancellation.
5. Consumer code (Aila) can type-assert `model.(CapabilityModel)` and handle the `!ok` path gracefully.
6. Metadata is optional and best-effort; the base `Model` interface remains unchanged.
7. The interface describes the selected adapter/model, not a global marketplace.
8. Product-specific model catalogs remain host-owned.

---

## GA-AILA-008: Replay-safe event serialization

**Priority:** Medium
**go-agent files:** `api.go:272-295`, `stream_assembly.go`

### What Aila needs

Aila saves run history to `.aila/` for session continuity, undo/redo, and cross-run context. The event stream must survive a `json.Marshal` / `json.Unmarshal` roundtrip. Aila's README states "`.aila/` is project state, not throwaway cache. Commit it" (`~/git/aila/README.md:96-99`).

Aila's state store tracks session history, source provenance, and compacted context (`~/git/aila/docs/workflow-architecture.md:96-107`). Aila history includes runs, edits, checks, undo data, and reviews (`~/git/aila/README.md:265-280`).

### Current go-agent behavior

`Event` (`api.go:272-295`) has an `Err error` field. Go's `error` type is an interface — `json.Marshal` of an error produces either an empty object `{}` or the error's `Error()` string. After unmarshal, the error type and chain are lost. This makes cross-process event replay unreliable.

Additionally, `ToolResult.JSON` is `any`, which may contain non-JSON-safe Go types (channels, functions, etc.), though in practice this is generally JSON-safe since tool input is parsed from JSON.

### Proposed solution

Add JSON-safe event serialization types alongside `Event`. The runtime `Event` itself must NOT change its `Err` field (that would break the streaming contract and all existing tests). Instead, provide conversion functions:

```go
// JSONEvent is a JSON-safe representation of Event. All fields are
// marshalable and produce a correct Event on Unmarshal + FromJSONEvent.
type JSONEvent struct {
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
	Usage          Usage
	Diagnostics    ProviderDiagnostics
	Decision       Decision
	PolicyDecision PolicyDecision
	Retry          RetryEvent
	StopReason     StopReason
	ErrorMessage   string
	ErrorType      string
}

// ToJSONEvent converts a runtime Event to a JSON-safe representation.
func ToJSONEvent(event Event) JSONEvent

// FromJSONEvent converts a JSON-safe event back to a runtime Event.
func FromJSONEvent(event JSONEvent) Event

// MarshalEvents serializes a slice of Events to JSON.
func MarshalEvents(events []Event) ([]byte, error)

// UnmarshalEvents deserializes JSON back to Events.
func UnmarshalEvents(data []byte) ([]Event, error)
```

`ToJSONEvent` stores `event.Err.Error()` in `ErrorMessage` and `fmt.Sprintf("%T", event.Err)` in `ErrorType` (when `Err` is non-nil). `FromJSONEvent` reconstructs a Go error via `errors.New(ErrorMessage)` for display purposes. The complete error chain and type identity are not preserved, but the error message and approximate type are retained — sufficient for historical display and debugging.

### Acceptance criteria

1. `json.Marshal(MarshalEvents(events))` produces valid JSON.
2. `UnmarshalEvents(jsonData)` followed by `MarshalEvents` produces the same JSON.
3. The error message is preserved across roundtrip (exact chain may be lost).
4. `FromJSONEvent` produces events that pass `AssembleEvents` without divergence.
5. All event kinds roundtrip correctly.
6. `JSONEvent` has no `Err` field — only `ErrorMessage` and `ErrorType` strings.
7. The contract documents which event fields are observational and which affect transcript assembly.
8. The format is versioned or otherwise migration-safe enough for local persisted histories.

---

## GA-AILA-009: Parent/child run event correlation for utility jobs and sub-agents

**Priority:** Medium
**go-agent files:** `api.go:28-37,272-295`, `runner.go:69-82`

### What Aila needs

Aila's orchestrate capability launches sub-agents to execute plan steps in parallel. The utility model worker runs background compaction and suggestion tasks. Both produce runs that need lineage — a child run must reference its parent, and events must be correlatable across parent and children.

The architecture states orchestrate "may dispatch workers, update plan status, request audits, and enforce retry budgets" (`~/git/aila/docs/workflow-architecture.md:304`). Aila plans sub-agents for aggressive parallelism and an `orchestrate` capability for autonomous plan execution with parallel agents, evaluation, and retries (`~/git/aila/README.md:76-82,218-231`).

### Current go-agent behavior

`RunRequest` has no `ParentRunID` or correlation metadata. `Event` has `RunID` and `TurnID` but no parent reference. Run IDs are auto-generated as `run-<seq>` in `runner.go:87`. There is no way to pass a pre-assigned run ID or link runs.

### Proposed solution

Add optional correlation fields to `RunRequest` and `Event`:

```go
type RunRequest struct {
	// ... existing fields ...

	// ParentRunID links this run to the run that spawned it.
	ParentRunID string
	// RunID overrides the auto-generated run identifier.
	RunID string
	// TaskID identifies a logical task that may span multiple runs.
	TaskID string
	// Metadata carries opaque host correlation data.
	Metadata map[string]string
}

type Event struct {
	// ... existing fields ...

	// ParentRunID links this event to a parent run when the current run
	// was spawned by another.
	ParentRunID string
	// TaskID identifies the logical task this event belongs to.
	TaskID string
}
```

In `runner.go`, the `runState` records parent correlation and stamps every event:

```go
func (r *runner) run(ctx context.Context, request RunRequest, emit func(Event)) (RunResult, error) {
	state := runState{
		runID:       runID(request.RunID),
		parentRunID: request.ParentRunID,
		taskID:      request.TaskID,
	}
}

func (s *runState) send(kind EventKind, payload eventPayload) {
	event := Event{
		Kind:         kind,
		ParentRunID: s.parentRunID,
		TaskID:      s.taskID,
	}
}
```

The `RunID` override skips auto-generation when set (must be non-empty). The runtime does not interpret parent/child relationships — it is purely correlation metadata. Hosts (Aila) own the semantics: cancellation propagation, result aggregation, tree visualization.

### Acceptance criteria

1. `RunRequest.RunID` (non-empty) sets the run identifier; auto-generation is skipped.
2. `RunRequest.ParentRunID` is stamped on every event emitted by that run.
3. `RunRequest.TaskID` is stamped on every event emitted by that run.
4. `AssembleEvents` preserves `ParentRunID` and `TaskID` through stream replay.
5. `RunResult.Events[n].ParentRunID` equals `RunRequest.ParentRunID`.
6. All fields are zero-value safe; empty strings produce no parent/task correlation (backward compatible).
7. Auto-generated run IDs remain unique even when `RunRequest.RunID` is used.
8. Event sinks can multiplex child-run events without guessing lineage.
9. The feature does not impose a sub-agent hierarchy or workflow DSL.

---

## GA-AILA-010: Run-level limits beyond model steps

**Priority:** Medium
**go-agent files:** `api.go:28-37`, `runner.go:84-181`

### What Aila needs

Aila's autonomy model and resource management need run-level limits beyond model turn count:

- **Max tool calls**: A misrouted capability should not generate unbounded tool invocations.
- **Max duration (wall clock)**: A run must not hang indefinitely.
- **Max tool output bytes**: Cumulative tool output must not exhaust memory.

Aila autonomy levels gate read/write/yolo behavior (`~/git/aila/README.md:152-159`). Permission decisions must be tied to exact proposal data and rechecked immediately before mutation (`~/git/aila/docs/workflow-architecture.md:558-576`). The TUI must keep active work observable (`~/git/aila/docs/workflow-architecture.md:605-620`).

### Current go-agent behavior

`RunRequest.MaxSteps` limits model turns (`runner.go:106-109`). There is no limit on tool calls per run, wall-clock duration, or cumulative output volume. Context cancellation is the only duration boundary. `ToolConstraints` supports per-tool timeout and max output bytes, but no aggregate enforcement exists.

### Proposed solution

Add a `RunLimits` struct to `RunRequest`:

```go
type RunLimits struct {
	MaxSteps           int
	MaxToolCalls       int
	MaxDuration        time.Duration
	MaxToolOutputBytes int64
}

type RunRequest struct {
	// ... existing fields ...

	// Limits constrains resource consumption for this run. Zero values mean
	// no explicit limit; defaults may be applied by the runner.
	Limits RunLimits

	// MaxSteps overrides Limits.MaxSteps when set. Deprecated in favor of
	// Limits.MaxSteps; preserved for backward compatibility.
	MaxSteps int
}
```

In `runner.go`, check limits at each tool call:

```go
func (r *runner) run(ctx context.Context, request RunRequest, emit func(Event)) (RunResult, error) {
	maxSteps := resolveMaxSteps(request)
	if request.Limits.MaxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Limits.MaxDuration)
		defer cancel()
	}
	limits := request.Limits
	toolCallCount := 0
	toolOutputBytes := int64(0)

	for step := 0; ; step++ {
		// ... existing step-limit check ...
		// ... model stream ...
		for _, call := range turn.assembled.ToolCalls {
			if limits.MaxToolCalls > 0 && toolCallCount >= limits.MaxToolCalls {
				return r.saveResult(ctx, r.finish(ctx, &state, turnID, StopToolCallLimit))
			}
			toolCallCount++
			// ... callTool ...
			if limits.MaxToolOutputBytes > 0 {
				toolOutputBytes += int64(len(result.Content))
				if toolOutputBytes > limits.MaxToolOutputBytes {
					return r.saveResult(ctx, r.finish(ctx, &state, turnID, StopOutputLimit))
				}
			}
		}
	}
}
```

New stop reasons:

```go
const (
	StopToolCallLimit StopReason = "tool_call_limit"
	StopOutputLimit   StopReason = "output_limit"
	StopDurationLimit StopReason = "duration_limit"
)
```

### Acceptance criteria

1. `RunLimits.MaxToolCalls` stops the run with `StopToolCallLimit` after the specified count.
2. `RunLimits.MaxDuration` cancels the context after the duration; run stops with `StopCanceled` or `StopDurationLimit`.
3. `RunLimits.MaxToolOutputBytes` stops with `StopOutputLimit` when cumulative tool output content exceeds the limit.
4. Zero-valued limits are ignored.
5. Limits work identically for `Run` and `Stream`.
6. `RunLimits.MaxSteps` takes precedence over `RunRequest.MaxSteps` when both are set.
7. Existing `MaxSteps` behavior remains backward-compatible.
8. Policy can inspect the active limits at run start and retry decisions.
9. Aggregate output limits are observable and do not silently truncate unless the host opted into truncation behavior.

---

## Explicit non-requests for go-agent

These Aila needs are deliberately NOT requested from go-agent. They belong in Aila's product layer, consistent with go-agent's stated library-first, no-workflow-DSL, no-platform direction.

| Aila need                                                                                                                                              | Why this is Aila-owned                                                                                                                                                                         |
| ------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `aila run`, `aila continue`, `aila config`, `aila models`, and Bubble Tea TUI                                                                          | go-agent explicitly leaves CLI/TUI/product shell behavior to the host (`~/git/go-agent/README.md:150-156,466-467`).                                                                            |
| Aila's six-state Agentera-derived FSM                                                                                                                  | go-agent explicitly rejects workflow DSL ownership (`~/git/go-agent/README.md:99-103,455-458`).                                                                                                |
| Capability adapters for `brief`, `vision`, `discuss`, `research`, `plan`, `build`, `optimize`, `document`, `design`, `audit`, `profile`, `orchestrate` | Aila intentionally embeds fixed Agentera-derived capabilities and does not support runtime capability loading (`~/git/aila/docs/workflow-architecture.md:405-456,651-658`).                    |
| `.aila/` artifact resolver and project memory layout                                                                                                   | go-agent provides `SessionStore`; product artifact mapping, provenance, compacted context, and committed state are Aila-specific (`~/git/aila/docs/workflow-architecture.md:335-404`).         |
| Provider registry, config files, environment variables, device-code auth, token exchange, and account selection                                        | go-agent explicitly excludes provider registry, credential discovery, auth assembly, and settings loading (`~/git/go-agent/README.md:150-156`).                                                |
| Built-in `read`, `edit`, `write`, `bash`, `grep`, `find`, and `fetch` implementations                                                                  | go-agent should expose runtime tool primitives; Aila owns the exact tool semantics, permission checks, history, compression, and workspace safety.                                             |
| Permission popup rendering and autonomy UI                                                                                                             | go-agent policy should stay host-owned; Aila owns the approval UI and autonomy vocabulary.                                                                                                     |
| Context compaction logic with provenance tracking                                                                                                      | Product-specific context strategy.                                                                                                                                                             |
| Utility model worker lifecycle                                                                                                                         | Aila controller manages concurrent runners.                                                                                                                                                    |
| MCP servers, plugins, hooks, skills, marketplace APIs, or user-defined capability schemas                                                              | Aila explicitly rejects these surfaces, and go-agent core also treats MCP/plugin/product shell behavior as outside core scope (`~/git/aila/README.md:14`, `~/git/go-agent/README.md:150-157`). |

## Suggested submission order

1. **GA-AILA-001** (run-scoped instructions and tools) — Foundational for the FSM; every phase needs different prompts and tool visibility.
2. **GA-AILA-002** (recoverable policy denials) — Must ship alongside GA-AILA-001 to make approval flows work without terminating runs.
3. **GA-AILA-003** (observable policy lifecycle) — Required for the TUI to render approval prompts; must be in place before permission-dependent tools are implemented at scale.
4. **GA-AILA-004 + GA-AILA-005** (streaming tools + rich results) — Needed before implementing Aila's shell, fetch, grep, and edit tools at scale.
5. **GA-AILA-006** (additional providers) — Split into separate provider-specific issues; `custom` is already covered by `providers/openai`.
6. **GA-AILA-007** (model capability metadata) — Required for the `/model` command, context window display, and compaction decisions.
7. **GA-AILA-008 + GA-AILA-009** (serialization + correlation) — Needed before `.aila/` committed state and orchestrate become relied-upon features.
8. **GA-AILA-010** (run limits) — Defense-in-depth; should be in place before autonomy level `yolo` becomes the default.
