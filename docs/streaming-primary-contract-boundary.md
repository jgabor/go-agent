# Streaming-Primary Contract Boundary

Date: 2026-05-07

This document records the intentional public breaks for the Streaming-Primary
Runtime Contract plan. It is a boundary declaration only; it does not implement
the runtime redesign.

Freshness note: this is a Task 1 historical boundary document. The
pre-migration facts below were true when the break list was written; later plan
tasks migrated the repository to the streaming-primary contract.

## Pre-Migration Contract Reviewed

The reviewed public surface at Task 1 time was turn-first:

| Surface         | Current shape                                                                                                                           | Evidence                     |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------- |
| `Runner.Run`    | Executes the runtime loop and returns `RunResult` directly from turn/tool execution.                                                    | `api.go`, `runner.go`        |
| `Runner.Stream` | Returns `<-chan Event` but internally runs the same non-streaming loop and emits synthesized runtime events.                            | `api.go`, `runner.go`        |
| `Model`         | `Turn(context.Context, TurnRequest) (TurnResult, error)` produces one whole model turn.                                                 | `api.go`                     |
| `TurnRequest`   | Carries instructions, `[]Message`, `[]ToolSpec`, and `Session`; no generic model options or usage seam.                                 | `api.go`                     |
| `TurnResult`    | Carries whole `Message`, separate `[]ToolCall`, and `StopReason`; no stream, usage, or accepted-stream failure semantics.               | `api.go`                     |
| `Message`       | One role plus string `Content`, optional `Name`, `ToolCallID`, and assistant `ToolCalls`.                                               | `api.go`                     |
| `ToolResult`    | Tool output is string `Content` only.                                                                                                   | `api.go`                     |
| Events          | `text_delta`, `tool_call`, `tool_result`, `policy_decision`, `retry`, `error`, and `stop` events with one broad `Event` payload struct. | `api.go`, `runner.go`        |
| Session         | `Session.Messages []Message` persists the current string-content transcript shape through host stores.                                  | `api.go`, `session.go`       |
| Provider        | `providers/openai.ChatModel` adapts non-streaming chat completions to `Model.Turn`.                                                     | `providers/openai/openai.go` |
| Docs/examples   | README and examples compile against `Model.Turn`, string messages, string tool results, and current event names.                        | `README.md`, `examples/*`    |

## Intentional Public Breaks

These are named breaks. Future compile failures in docs, examples, tests, or
callers caused by these changes are expected consequences of the redesign, not
accidental drift.

| Surface                      | Intentional break                                                                                                                                    | Target boundary                                                                                                                                                          |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `Model` method               | Replace `Turn(context.Context, TurnRequest) (TurnResult, error)` as the primary provider seam.                                                       | Provider implementations emit canonical `Event` streams; completion is assembled from that stream.                                                                       |
| `TurnRequest` fields         | Add generic runtime/provider options and remove the assumption that a turn request is only instructions, messages, tools, and session.               | Options may cover generic output limits, sampling, reasoning effort, stop sequences, response format, and bounded provider metadata.                                     |
| `TurnResult` type            | Stop treating whole-turn `TurnResult` as the canonical model output.                                                                                 | Final response/result values are projections from canonical events. A simple-model adapter may preserve ergonomic test/fake models without keeping `TurnResult` primary. |
| `Runner.Run` semantics       | Change from a separate completion path to stream assembly.                                                                                           | `Run` consumes the same canonical event sequence as `Stream` and returns the assembled final result plus Go errors according to terminal stream semantics.               |
| `Runner.Stream` semantics    | Change from a best-effort event channel over a non-streaming model loop to the authoritative runtime stream.                                         | `Stream` exposes canonical event ordering, accepted-turn terminal error/stop events, and enough data to reconstruct final messages/results.                              |
| `RunResult` fields           | Expand beyond `Text`, `StopReason`, `Session`, and `Events` as needed for assembled messages and usage metadata.                                     | Final result is assembled from canonical events and may expose block messages and generic usage metadata.                                                                |
| `Message` fields             | Break `Content string`, `ToolCallID`, and `ToolCalls []ToolCall` as the transcript grammar.                                                          | `Message` becomes block-based, representing text, tool-call, and tool-result blocks without provider-specific parsing.                                                   |
| `ToolResult.Content`         | Break string-only tool results.                                                                                                                      | Tool results become JSON-compatible so text and structured values can be preserved through messages, events, policy, and sessions.                                       |
| `EventKind` names            | Current event names are not guaranteed stable.                                                                                                       | Task 2 will define canonical event names for start/delta/end/final message, tool-call deltas/finalization, tool results, usage, errors, and stops.                       |
| `Event` fields               | Break the broad payload assumption that one struct with `Text`, `Message`, `ToolCall`, `ToolResult`, `Retry`, `StopReason`, and `Err` is sufficient. | Event payloads must support block deltas, correlation, terminal semantics, usage, and deterministic reduction without contradictory terminal facts.                      |
| Accepted stream failures     | Current model errors occur before a returned turn; `Stream` hides `run` errors inside the goroutine after channel creation.                          | Setup failures remain Go errors; accepted-turn stream failures return Go errors and also emit terminal error/stop events for consumers.                                  |
| Session transcript shape     | Existing `Session.Messages []Message` snapshots using string content become incompatible with the block transcript.                                  | Host stores must either migrate records to the new block shape or reject old records explicitly. Core will not guess a migration for unknown persisted data.             |
| Event persistence shape      | Any external event log that stored current `EventKind` names or payload fields becomes incompatible.                                                 | Persisted event replay is only supported for the new canonical grammar once defined; old event logs are unsupported without a compatibility owner.                       |
| `providers/openai.ChatModel` | Current non-streaming chat-completions adapter no longer satisfies the primary provider seam as-is.                                                  | Provider proof must normalize OpenAI-compatible streaming text, tool calls, usage, stops, setup failures, and accepted-turn failures into canonical events.              |
| Docs/examples                | Current examples implement `Model.Turn` and inspect `Message.Content`/current event names.                                                           | Examples must be updated in later tasks only after the new contract is implemented; compile failures meanwhile should map to the named breaks above.                     |

## Persistence Classification

| Old shape                                                               | Classification                                 | Rationale                                                                                                                                             |
| ----------------------------------------------------------------------- | ---------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| In-process `MemorySessionStore` values using old `Message`              | Migrated by code change only                   | The memory store has no durable compatibility boundary; once types change, in-process values are rebuilt by current code/tests.                       |
| Host-owned persisted `Session.Messages` with string content/tool fields | Rejected unless host migrates                  | The core has no concrete compatibility owner or schema/version source for external stores, so automatic migration would guess at host data semantics. |
| Stored `RunResult.Events` slices with current event names/payloads      | Unsupported                                    | go-agent has no event-store API today, and external event persistence is outside the current compatibility contract.                                  |
| Event sink downstream records using current names/fields                | Unsupported by core                            | Sinks are observational. Hosts that persisted sink output own migration to the new canonical grammar.                                                 |
| README/example fake models implementing `Model.Turn`                    | Migrated in later implementation tasks         | These are repository-owned compile targets and should move to the simple-model adapter or streaming model contract after the grammar is implemented.  |
| `providers/openai.ChatModel` current behavior                           | Migrated in later provider-proof/runtime tasks | The existing provider package is repository-owned and must be updated to prove the new event grammar.                                                 |

## Explicit Non-Goals

The streaming-primary contract does not move product/provider policy into core.
These remain outside go-agent core for this plan:

| Non-goal                                           | Boundary                                                                                             |
| -------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| Provider registry and provider selection           | Hosts choose and configure provider adapters.                                                        |
| Auth, credentials, token exchange, and approval UI | Hosts resolve credentials and product auth flows before constructing runtime dependencies.           |
| Settings loading                                   | Hosts load settings and pass resolved options/dependencies.                                          |
| Prompts, skills, and resources                     | Hosts assemble instructions and resources; core accepts runtime values only.                         |
| CLI shell behavior and CLI subprocess providers    | Examples may consume the library, but core does not own product shell or hidden shell execution.     |
| MCP                                                | Adapter packages may exist outside core; no core requirement.                                        |
| Sub-agents                                         | Orchestration shape remains host-owned.                                                              |
| Workflow DSL                                       | Go remains the orchestration language; no workflow DSL in core.                                      |
| Workdir behavior                                   | Host/provider-specific concern, especially for CLI subprocess providers.                             |
| Pricing and cost policy                            | Generic usage metadata may be represented; pricing and budget/product policy remain host-owned.      |
| Lira policy                                        | Lira keeps registry, auth, CLI providers, workflow recovery, pricing, workdir, and product behavior. |

## Review Notes

Reviewed current public contracts in `api.go`, runtime behavior in `runner.go`,
tool/session/provider shapes in `tool.go`, `session.go`, and
`providers/openai/openai.go`, package docs in `doc.go`, README usage/roadmap
sections, the Lira gap analysis, and all runnable examples. No runtime redesign
was implemented in this task.
