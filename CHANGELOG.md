# Changelog

## [Unreleased]

### Added

- Runtime seams for downstream hosts: per-run instructions and registered-tool subset (`ToolNames`), aggregate `RunLimits`, versioned JSON event persistence with optional run correlation fields, policy pending events and recoverable tool-call denials, rich `ToolResult` facts with validation, optional `StreamingTool` progress events, and optional `ModelCapabilitiesOf` hints (`openai.ChatModel`).
- Added `NewRunner` implementing the core agent loop with model turns, tool dispatch, policy checks, event emission, session tracking, step limits, and cancellation.
- Added expanded policy decisions for run-start constraints, tool-call constraints, and tool-result validation with structured policy events.
- Added `providers/openai` with an OpenAI-compatible chat completions model adapter.
- Added `EventSink` hooks for optional logging, tracing, UI, replay, and OpenTelemetry adapters.
- Added a runnable minimal app example using `New`, `NewTool`, a local model, and event sinks.
- Added a runnable HTTP service example embedding the runtime with sessions, policy, and event sinks.
- Added a runnable worker example with background jobs, deadlines, cancellation, sessions, and event logging.
- Added a runnable CLI consumer example with final-text and structured-event output modes.
- Added the low-ceremony `New` constructor facade for resolved runtime dependencies.
- Added typed retry policy and event semantics with policy-governed tool retry gated by retry-safety metadata.
- Added opt-in observable retry for model and session-load runtime failures with policy-visible retry decisions and structured retry events.
- Added `ToolDefinition` for advanced tool schema, safety, and execution-constraint metadata exposed through model requests, policy decisions, and tool events while tool retry execution remains inactive.
- Added streaming behavior tests covering multi-turn tool dispatch, event ordering and correlation, error stops, cancellation, and Run/Stream parity.
- Added struct-input tool schema generation and `NewToolWithSchema` for explicit schema metadata.
- Added `SessionStore`, `RunRequest.SessionID`, and `NewMemorySessionStore` for resumable pluggable session storage.
- Added `NewTool` for adapting minimal `func(context.Context, string) (string, error)` tools with name and input validation.
- Added runtime behavior contract tests for completion, tool dispatch, tool errors, model errors, cancellation, policy denial, step limits, event ordering, and stop reasons.
- Added the root public API contract for agents, runners, models, tools, sessions, events, and policy decisions.

### Changed

- Updated the roadmap from deferred retry behavior to started typed retry semantics while runtime execution remains a later slice.
- Updated the roadmap to reflect the started public API and contract test slices.
