# Streaming-Primary Event Grammar

Date: 2026-05-07

This document specifies the target event and block-message grammar for the
Streaming-Primary Runtime Contract. It is a specification only; it does not
migrate runtime behavior, providers, sessions, examples, or tests to the new
contract.

## Goals

The grammar makes `Event` the canonical streaming boundary and makes `Message`
the durable transcript projection of that stream. Provider adapters normalize
wire-specific payloads before emitting these events. Reducers, sessions,
policies, and callers should not parse OpenAI, Anthropic, Lira, or any other
provider-specific stream format to understand transcript text, tool calls, tool
results, usage, stops, or accepted stream failures.

The grammar must remain runtime-only. It allows generic model options and usage
metadata, but excludes registry, auth, settings, prompt/resource loading, CLI
shell behavior, MCP, sub-agents, workflow DSLs, workdir behavior, pricing, and
Lira product policy.

## Block Messages

`Message` becomes an ordered list of content blocks. The message role identifies
the speaker; blocks identify the content carried by that speaker.

| Field    | Semantics                                                                                    |
| -------- | -------------------------------------------------------------------------------------------- |
| `Role`   | `system`, `user`, `assistant`, or `tool`.                                                    |
| `Name`   | Optional host/provider-neutral participant name.                                             |
| `Blocks` | Ordered `[]Block`; empty only for explicit no-content messages when a provider requires one. |

Block kinds:

| Kind          | Valid roles                           | Required fields                                                 | Semantics                                                                                                                    |
| ------------- | ------------------------------------- | --------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| `text`        | `system`, `user`, `assistant`, `tool` | `Text`                                                          | Literal transcript text. No provider-specific chunk parsing is required after assembly.                                      |
| `tool_call`   | `assistant`                           | `ToolCall.ID`, `ToolCall.Name`, `ToolCall.Arguments`            | Assistant request to execute a tool. `Arguments` is the complete JSON byte sequence after stream finalization.               |
| `tool_result` | `tool`                                | `ToolResult.CallID`, `ToolResult.Name`, one of `Text` or `JSON` | Result returned for a prior tool call. `JSON` is any JSON-compatible value: object, array, string, number, boolean, or null. |

Text blocks may coexist with tool-call blocks in one assistant message. Multiple
tool-call blocks in one assistant message preserve provider order through their
block order and through each block's stable `ToolCall.ID`. Tool-result messages
may contain one or more tool-result blocks; when a provider requires one message
per result, the projection may still store one block per message.

Tool-call IDs are runtime-visible correlation IDs. Providers that supply IDs
must preserve them. Providers that stream a call without an ID must receive a
runtime-generated stable ID before any tool-call delta is emitted. The generated
ID is part of the normalized grammar and must be reused for later deltas,
finalization, execution, result, policy, and error events.

Tool-result blocks are JSON-capable but not schema-registry-aware. The core does
not attach MIME resources, MCP content blocks, marketplace metadata, pricing, or
Lira cost policy to tool results.

## Event Envelope

Every accepted run-stream event has an envelope:

| Field         | Semantics                                                                                                                                                                  |
| ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Sequence`    | Run-local monotonically increasing integer assigned by the runtime after acceptance.                                                                                       |
| `RunID`       | Stable ID for one runtime run.                                                                                                                                             |
| `TurnID`      | Stable ID for one model assistant response attempt when applicable.                                                                                                        |
| `Kind`        | One canonical event kind from this grammar.                                                                                                                                |
| `MessageID`   | Stable ID for the assistant or tool message being assembled when applicable.                                                                                               |
| `BlockID`     | Stable ID for a block being assembled when applicable.                                                                                                                     |
| `ToolCallID`  | Stable ID for tool-call deltas, final calls, execution, results, and related errors when applicable.                                                                       |
| `Payload`     | Kind-specific payload.                                                                                                                                                     |
| `Diagnostics` | Optional bounded non-secret provider diagnostics: provider/package identifiers, request ID, HTTP status, provider error type/code, raw stop reason, and sanitized excerpt. |

Envelope diagnostics are observational and closed. They must not contain
credentials, authorization state, full prompts/messages, unredacted tool
arguments, environment values, credential-bearing URLs, registry/provider
selection policy, persisted settings, pricing, budget policy, marketplace
metadata, workdir behavior, or Lira policy. The core diagnostics surface must
not include arbitrary maps or opaque provider/host metadata.

## Event Kinds

| Kind                  | Required payload                                                                           | Semantics                                                                                                                                                                                       |
| --------------------- | ------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `response_start`      | `Role=assistant`, optional initial `MessageID`                                             | The provider/runtime has accepted a model response stream for this turn. No assistant content for the turn may precede it.                                                                      |
| `content_block_start` | `BlockID`, block kind                                                                      | Starts an assistant text or tool-call block.                                                                                                                                                    |
| `text_delta`          | `BlockID`, `TextDelta`                                                                     | Appends literal text to an open text block.                                                                                                                                                     |
| `tool_call_delta`     | `BlockID`, `ToolCallID`, optional `Index`, optional `NameDelta`, optional `ArgumentsDelta` | Appends incremental tool-call name and/or raw JSON argument bytes to an open tool-call block.                                                                                                   |
| `content_block_end`   | `BlockID`, optional finalized block                                                        | Ends an open text or tool-call block. For tool calls, this is finalization before execution eligibility.                                                                                        |
| `message_final`       | Complete assistant `Message`                                                               | The assistant message assembled for the turn. It must match prior block starts, deltas, and ends.                                                                                               |
| `tool_call_ready`     | Complete `ToolCall`                                                                        | A finalized tool call is ready for policy/execution. Emitted after its tool-call block is complete and before the tool executes.                                                                |
| `tool_result`         | Complete `ToolResult` and tool-result `Message` or block                                   | A tool execution result is appended to the transcript.                                                                                                                                          |
| `usage`               | `Usage`                                                                                    | Typed token/cache/request/provider/model facts for a completed or terminal provider interaction, including reasoning-token counts when reported.                                                |
| `stop`                | `StopReason`                                                                               | Terminal accepted-run stop state. Exactly one terminal stop is emitted for an accepted run.                                                                                                     |
| `error`               | `Error` and terminal classification                                                        | Terminal accepted-run error state, or non-terminal diagnostic only when explicitly marked non-terminal by a future extension. In this plan, accepted stream failures use terminal error events. |

Policy and retry events from the current runtime remain runtime decision events,
not provider stream grammar. Later implementation tasks may keep or reshape them,
but they must reduce without contradicting the canonical response, tool, usage,
stop, and error events above.

`Message.HiddenReasoning` is replay-only provider state, not a user-facing block
kind. Providers that require hidden reasoning replay keep it attached to the
assistant message that produced it; reducers must not turn it into text blocks or
tool-result content.

## Ordering Rules

Setup failures occur before a provider stream is accepted. They return ordinary
Go errors and emit no accepted-turn transcript events.

Once a turn is accepted, the ordering rules are:

1. `response_start` is first for that assistant response.
2. `content_block_start` precedes deltas for the same `BlockID`.
3. `text_delta` and `tool_call_delta` preserve provider order per block.
4. `content_block_end` follows all deltas for that `BlockID`.
5. `message_final` follows every ended assistant content block for that response.
6. `tool_call_ready` follows finalization of the corresponding tool-call block and precedes execution of that call.
7. `tool_result` follows execution and policy acceptance of that call's result.
8. `usage` is emitted when typed usage facts are known. It may appear before `stop`, after `message_final`, or after a terminal `error` if the provider supplies usage with an error, but it cannot appear after `stop`.
9. `error`, when terminal, may occur only before `stop`. After a terminal `error`, the only allowed events are optional `usage` facts and the required terminal `stop`; no content, tool-call, tool-result, final-message, or second terminal event may follow it.
10. `stop` is the final accepted-run event. No event of any kind may follow `stop`, including `error`.

`message_final`, `usage`, `error`, and `stop` must not contradict each other. A
reducer must reject streams where a final message claims content not produced by
the block events, a stop reason claims success after a terminal error, usage
appears after stop, or terminal events appear more than once.

## Tool-Call Streaming

Tool-call streaming is represented by a tool-call block plus deltas. The
grammar treats a streamed tool call as incomplete until the matching
`content_block_end` finalizes it.

The tool-call delta payload supports:

| Field            | Semantics                                                                                                                                                                     |
| ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ToolCallID`     | Stable correlation ID. Required on every normalized tool-call delta, generated by the runtime if the provider has not supplied one yet.                                       |
| `Index`          | Optional provider-neutral ordinal for concurrent calls in the same response. Used only to help normalization and diagnostics; `ToolCallID` is authoritative after assignment. |
| `NameDelta`      | Optional name fragment. Multiple fragments concatenate to the final tool name.                                                                                                |
| `ArgumentsDelta` | Optional raw JSON argument byte fragment. Multiple fragments concatenate to the final arguments.                                                                              |

Multiple concurrent tool calls are represented by multiple open tool-call blocks
with distinct `BlockID` and `ToolCallID` values. Deltas for different calls may
interleave. Per-call order is reconstructed by `(RunID, TurnID, ToolCallID,
Sequence)`; message order is reconstructed by the order each block starts in the
assistant message. A call is executable only after its block ends and the
assembled name is non-empty and its arguments form complete valid JSON.

Malformed streams are terminal accepted-stream failures:

| Malformation                                                          | Required outcome                                                                            |
| --------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| Delta for unknown `BlockID` or `ToolCallID`                           | Emit terminal `error`, then terminal `stop` with a model/provider stream error stop reason. |
| Duplicate `content_block_end` for a block                             | Emit terminal `error`, then terminal `stop`.                                                |
| Tool-call block ends with empty name                                  | Emit terminal `error`, then terminal `stop`; no execution.                                  |
| Tool-call arguments are not complete valid JSON at block end          | Emit terminal `error`, then terminal `stop`; no execution.                                  |
| `message_final` omits or changes finalized tool calls                 | Emit terminal `error`, then terminal `stop`; no execution for changed calls.                |
| Stream ends before open blocks are closed or before `message_final`   | Emit terminal `error`, then terminal `stop`; no implicit final message.                     |
| Provider finalizes a tool call before all name/argument deltas arrive | Treat later deltas for that call as malformed and terminal.                                 |

Finalization before execution is explicit: `content_block_end` makes the block
closed, `message_final` makes the assistant message complete, and
`tool_call_ready` marks each complete call eligible for policy and execution.
Execution must not start from partial name or argument deltas.

## Usage Facts

`Usage` is a closed set of runtime/provider accounting facts. It is
descriptive, not policy enforcement.

Allowed fields:

| Field               | Semantics                                                                   |
| ------------------- | --------------------------------------------------------------------------- |
| `InputTokens`       | Provider-reported input token count.                                        |
| `OutputTokens`      | Provider-reported output token count.                                       |
| `TotalTokens`       | Provider-reported total when available; reducers may derive only when safe. |
| `CachedInputTokens` | Input tokens served from provider cache.                                    |
| `CacheWriteTokens`  | Tokens written to provider cache.                                           |
| `ReasoningTokens`   | Provider-reported reasoning token count; zero means absent or zero.         |
| `RequestID`         | Non-secret provider request correlation ID.                                 |
| `Provider`          | Optional provider/package identifier for diagnostics.                       |
| `Model`             | Optional concrete model identifier reported by the provider.                |

Excluded fields and semantics: arbitrary metadata maps, replay payloads, cost,
price, currency, budget policy, spend limits, marketplace metadata, package
registry metadata, Lira cost policy, and any host product billing
interpretation. Hosts may compute cost outside core from usage plus their own
pricing tables, but that is not part of this grammar.

## Turn Options

Core turn options are provider-neutral runtime/provider hints. They are optional
and narrow so the core does not become a provider registry, settings store, or
provider-specific pass-through surface.

Allowed fields:

| Field             | Semantics                                             |
| ----------------- | ----------------------------------------------------- |
| `MaxOutputTokens` | Maximum generated output tokens requested for a turn. |
| `Temperature`     | Sampling temperature.                                 |
| `StopSequences`   | Literal stop sequences requested by the host.         |

Excluded options and semantics: credentials, auth flows, registry/provider
selection, persisted settings, pricing, budgets, marketplace metadata, workdir
behavior, prompt/resource loading, CLI shell configuration, MCP configuration,
sub-agent orchestration, workflow DSL controls, and Lira policy.

Non-universal provider knobs do not belong in core turn options or arbitrary
maps. OpenAI Chat Completions controls such as reasoning effort, response
format, and stream usage inclusion must live in bounded typed provider-package
options for a concrete adapter that has already been selected by the host.

## Reducer Contract

A deterministic reducer consuming this grammar must be able to reconstruct:

| Output                   | Source events                                                                                     |
| ------------------------ | ------------------------------------------------------------------------------------------------- |
| Assistant message blocks | `response_start`, block starts/deltas/ends, and `message_final`.                                  |
| Final text               | Ordered assistant text blocks in final messages.                                                  |
| Tool calls               | Finalized tool-call blocks and `tool_call_ready`.                                                 |
| Tool results             | `tool_result` events and their tool-result blocks.                                                |
| Usage                    | Latest non-contradictory `usage` events for the run/turn.                                         |
| Stop reason              | The single terminal `stop`.                                                                       |
| Accepted stream error    | Terminal `error` paired with terminal `stop` and returned as a Go error by completion-style APIs. |

Reducers must fail deterministically on contradictory, malformed, or incomplete
streams rather than guessing provider intent. Completion-style APIs are
projections over this reducer; they do not define a separate result grammar.

## Non-Goals

This grammar intentionally excludes provider registry, provider selection,
auth, settings, prompt/resource loading, CLI shell behavior, MCP, sub-agent
orchestration, workflow DSLs, workdir behavior, pricing, budget policy,
marketplace metadata, and Lira policy. Provider adapters and host applications
may own those concerns outside the core runtime.
