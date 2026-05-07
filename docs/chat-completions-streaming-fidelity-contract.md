# Chat Completions Streaming Fidelity Contract

Date: 2026-05-07

This contract locks the implemented OpenAI-compatible Chat Completions streaming
fidelity boundary. It documents the adapter behavior now implemented in
`providers/openai.ChatModel.Stream`; it does not add provider registry behavior,
auth discovery, pricing, or Lira workflow policy.

## Scope

The adapter scope is one already-selected OpenAI-compatible Chat Completions
endpoint using `POST /chat/completions` with `stream: true`. The provider
normalizes Chat Completions SSE chunks into go-agent's canonical `Event` grammar
from `docs/streaming-primary-event-grammar.md` and the runtime assembles
completion results from those events.

Included behavior:

| Area           | Contract                                                                                                                                                                    |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| API family     | OpenAI-compatible Chat Completions only.                                                                                                                                    |
| Transport      | HTTP response body containing Server-Sent Events with `data:` records and terminal `data: [DONE]`.                                                                          |
| Choices        | One active assistant choice per request. The adapter may reject multiple simultaneous choices instead of merging them.                                                      |
| Text           | Incremental `delta.content` chunks become canonical text block deltas.                                                                                                      |
| Tool calls     | Incremental `delta.tool_calls` chunks are accumulated by provider `index` and normalized to stable `ToolCallID` values.                                                     |
| Usage          | Provider-supplied usage chunks become `EventUsage`; absence of usage is valid.                                                                                              |
| Finish reasons | Raw provider finish reasons are diagnostics only; canonical behavior is expressed through final message/tool readiness, usage, terminal error, and stop events.             |
| Diagnostics    | Only bounded, non-secret provider facts are carried: provider/package identity, request ID, HTTP status, provider error type/code, raw stop reason, and sanitized excerpts. |

Excluded behavior:

| Non-goal                        | Reason                                                                                                                     |
| ------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| OpenAI Responses                | Different API and stream event grammar; explicitly outside this plan slice.                                                |
| Anthropic Messages              | Different provider family; explicitly outside this plan slice.                                                             |
| Copilot token exchange          | Product auth and routing policy owned by Lira or a host.                                                                   |
| Zen routing                     | Product/provider routing policy owned by Lira or a host.                                                                   |
| Provider registry or selection  | `go-agent` receives an already constructed model.                                                                          |
| Auth discovery, cache, or login | The host supplies credentials; the provider package must not find or persist them.                                         |
| CLI subprocess providers        | Host shell behavior, workdir, prompt transport, and CLI output parsing are not library runtime concerns.                   |
| Workdir behavior                | Product/provider execution policy outside Chat Completions SSE normalization.                                              |
| Pricing or cost                 | Hosts may compute cost externally from usage; `go-agent` does not encode prices, currencies, budgets, or Lira cost policy. |
| Lira workflow policy            | Lira recovery, budgets, provider config, and workflow semantics remain Lira-owned.                                         |

## Supported SSE Chunk Shapes

The supported stream is the OpenAI-compatible Chat Completions shape:

```text
data: {"choices":[{"delta":{...},"finish_reason":null}],"usage":null}
data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}
data: [DONE]
```

Supported records:

| SSE record                                          | Required handling                                                                                                                                                                             |
| --------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Blank lines and comments                            | Ignore without emitting canonical events.                                                                                                                                                     |
| `data: [DONE]`                                      | End the provider stream after all prior finalization has completed.                                                                                                                           |
| JSON chunk with `choices[0].delta.role="assistant"` | Accept as stream-start evidence; do not emit provider-specific role events.                                                                                                                   |
| JSON chunk with `choices[0].delta.content`          | Start a text block if needed, then emit `EventTextDelta` with literal text. Empty strings are no-ops unless needed for diagnostics.                                                           |
| JSON chunk with `choices[0].delta.tool_calls[]`     | Start or reuse a tool-call block by provider `index`; preserve supplied `id`, generate one if absent, append `function.name` and `function.arguments` fragments through `EventToolCallDelta`. |
| JSON chunk with `choices[0].finish_reason`          | Finalize open blocks and the assistant message according to the raw reason; retain the raw reason only as bounded diagnostics.                                                                |
| JSON chunk with empty `choices` and `usage`         | Emit usage only; this covers `stream_options.include_usage` style final usage chunks.                                                                                                         |
| JSON chunk with both `finish_reason` and `usage`    | Finalize the assistant turn and emit usage before any terminal stop.                                                                                                                          |
| Malformed JSON or contradictory chunks              | Emit an accepted-stream terminal error and terminal stop when the stream had been accepted; return the same Go error.                                                                         |

Supported tool-call delta fields:

| Provider field                    | Canonical mapping                                                                                                                                 |
| --------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `tool_calls[].index`              | `ToolCallDelta.Index`; normalization key until a stable `ToolCallID` exists.                                                                      |
| `tool_calls[].id`                 | Preserved as `Event.ToolCallID` and final `ToolCall.ID`; if absent, generate a stable ID before emitting the first delta for that provider index. |
| `tool_calls[].type`               | Must be `function` when present; other types are malformed for this adapter.                                                                      |
| `tool_calls[].function.name`      | Append to `ToolCallDelta.NameDelta`.                                                                                                              |
| `tool_calls[].function.arguments` | Append raw bytes to `ToolCallDelta.ArgumentsDelta`; validate complete JSON only at block finalization.                                            |

Unsupported or malformed chunks are terminal accepted-stream failures once a
`response_start` event has been emitted. Before stream acceptance, they are setup
failures and return ordinary Go errors without accepted-turn transcript events.

## Canonical Event Mapping

Provider-specific chunk, choice, delta, and finish-reason fields must not leak as
public event fields. They normalize into existing canonical events:

| Provider condition                       | Canonical events                                                                                                               |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| First accepted assistant stream evidence | `EventResponseStart` with a stable `MessageID`.                                                                                |
| First text delta                         | `EventContentBlockStart` with `BlockKind=BlockText`, then `EventTextDelta`.                                                    |
| Additional text delta                    | Additional `EventTextDelta` on the same text block.                                                                            |
| First tool-call delta for an index       | `EventContentBlockStart` with `BlockKind=BlockToolCall`, stable `BlockID`, and stable `ToolCallID`, then `EventToolCallDelta`. |
| Additional tool-call delta for an index  | Additional `EventToolCallDelta` on the same tool-call block.                                                                   |
| Finish after text                        | `EventContentBlockEnd` for the text block, then `EventMessageFinal`.                                                           |
| Finish after tool calls                  | `EventContentBlockEnd` for every tool-call block, then `EventMessageFinal`, then one `EventToolCallReady` per finalized call.  |
| Usage supplied                           | `EventUsage` with generic token/cache/request metadata.                                                                        |
| Accepted provider stream failure         | `EventError` with the provider error, optional `EventUsage` if supplied, then `EventStop` with a non-success stop reason.      |

Successful completed assistant-turn streams emit a canonical `EventStop` with
`StopComplete` after any provider-supplied usage. Tool-call turns do not emit a
provider-side success stop before `Runner` executes the tools. Accepted provider
stream failures emit terminal `EventError` and `EventStop` events.

## Terminal Semantics

Setup failures occur before `EventResponseStart`. Examples are missing model
configuration, missing API key, HTTP request construction failure, credential
absence supplied by the host, non-2xx response before SSE acceptance, or a body
that cannot be treated as an accepted SSE stream. Setup failures return Go errors
and emit no accepted assistant transcript events.

Accepted streams begin with `EventResponseStart`. After that point:

| Terminal path                                                                                                                                    | Required behavior                                                                                                                                                                                            |
| ------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `finish_reason="stop"`                                                                                                                           | Close open text/tool blocks, emit `EventMessageFinal`, emit `EventUsage` if supplied, emit `EventStop` with `StopComplete`, and return nil.                                                                  |
| `finish_reason="tool_calls"`                                                                                                                     | Close tool-call blocks, emit `EventMessageFinal`, emit `EventToolCallReady` for each valid call, emit `EventUsage` if supplied, and return nil without a provider-side success stop; runtime executes tools. |
| `finish_reason="length"`, `"content_filter"`, or another non-success reason                                                                      | Preserve raw reason as diagnostics; if canonical assembly cannot produce a safe final message, emit terminal `EventError` plus `EventStop` and return the same Go error.                                     |
| Provider closes before `[DONE]` after a valid finish                                                                                             | Treat as complete if the assistant turn is already finalized and no scanner/read error occurred.                                                                                                             |
| EOF, scanner error, malformed JSON, invalid tool arguments, changed tool IDs, duplicate finalization, or incomplete open blocks after acceptance | Emit terminal `EventError`, optional `EventUsage`, `EventStop`, and return the same Go error.                                                                                                                |

No event may follow `EventStop`. After terminal `EventError`, only optional
`EventUsage` and the required terminal `EventStop` may follow.

## Usage Behavior

Usage is descriptive provider metadata, not policy enforcement.

| Provider usage field               | Canonical field                                                                                                    |
| ---------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `prompt_tokens`                    | `Usage.InputTokens`                                                                                                |
| `completion_tokens`                | `Usage.OutputTokens`                                                                                               |
| `total_tokens`                     | `Usage.TotalTokens` when supplied; derive only when the provider omits it and both components are present.         |
| cache details                      | `Usage.CachedInputTokens` or `Usage.CacheWriteTokens` when the provider reports compatible non-secret token facts. |
| request/model/provider identifiers | `Usage.RequestID`, `Usage.Provider`, and `Usage.Model`.                                                            |

Absence of usage is valid. The adapter must not synthesize token counts from
text length, must not synthesize cost, and must not encode pricing, budgets,
currency, marketplace metadata, arbitrary usage metadata maps, or Lira cost
policy.

## Diagnostics Boundary

Diagnostics are observational and bounded. They may support debugging and replay,
but they must not become provider control state.

Allowed diagnostics:

| Diagnostic                | Boundary                                                                                                |
| ------------------------- | ------------------------------------------------------------------------------------------------------- |
| Provider/package identity | Literal adapter identity such as `openai-compatible` and `github.com/jgabor/go-agent/providers/openai`. |
| Request ID                | Non-secret response header or provider field used for correlation.                                      |
| HTTP status               | Numeric status for failed HTTP responses.                                                               |
| Provider error type/code  | Bounded string fields from provider error payloads.                                                     |
| Raw stop reason           | Raw Chat Completions `finish_reason`, for diagnostics only.                                             |
| Bounded excerpts          | Short, redacted, non-secret response excerpts for malformed provider responses.                         |

Forbidden diagnostics:

| Forbidden data                                                                                                                                          | Reason                                                        |
| ------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| API keys, bearer tokens, auth headers, credential file paths, or auth cache contents                                                                    | Secret material and host auth ownership.                      |
| Full prompts, messages, tool arguments, or tool results                                                                                                 | Transcript/tool payloads are not diagnostics.                 |
| Environment values, credential-bearing URLs, or raw request bodies                                                                                      | Secret and host config leakage risk.                          |
| Provider registry, selected-provider policy, settings, prompt/resource loading, workdir, pricing, budgets, marketplace metadata, or Lira workflow facts | Product policy and configuration are outside `go-agent` core. |

## Implemented Narrow Fields

The canonical event grammar is sufficient for text deltas, indexed tool-call
deltas, message finalization, tool-call readiness, typed usage, terminal errors,
and stream assembly. `ModelFromSimple` and `StreamTurnResult` remain preserved as
the ergonomic final-response adapter; completion assembly continues to reduce
canonical events rather than provider chunks.

The narrow fields added for this contract are:

| Gap                    | Narrow need                                                                                                                                                                                                                    |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Request options        | `TurnRequest.Options` carries provider-neutral max output tokens, temperature, and stop sequences. OpenAI-specific reasoning effort, response format, and stream usage inclusion live in typed `providers/openai.ChatOptions`. |
| General diagnostics    | `Event.Diagnostics` carries bounded request ID, HTTP status, provider error type/code, raw stop reason, and redacted excerpts without arbitrary maps.                                                                          |
| Provider error details | `ProviderError` carries typed diagnostics for provider status/code/request correlation.                                                                                                                                        |

No registry, auth discovery, settings loader, pricing/cost policy, workdir,
Responses API, Anthropic adapter, Copilot exchange, Zen routing, MCP,
sub-agent, workflow DSL, hosted platform, or Lira workflow behavior was added.
