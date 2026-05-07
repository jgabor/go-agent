# OpenAI-Compatible Provider Grammar Proof

Date: 2026-05-07

This document records the OpenAI-compatible provider proof from the
Streaming-Primary Runtime Contract work. It began as pre-migration evidence and
is now historical context for the implemented streaming-primary seam.

Repository reality has since moved on: Task 5 migrated the public model seam to
`Model.Stream(ctx, TurnRequest, emit) error`, and the current
`providers/openai.ChatModel` implements `goagent.Model` through
`ChatModel.Stream`. The Chat Completions Fidelity work then replaced the shallow
complete-response adapter path with direct Chat Completions SSE parsing.

## Proof Scope

The focused proof in `providers/openai/streaming_grammar_proof_test.go` models
OpenAI-compatible chat-completions streaming behavior and normalizes it into the
draft grammar from `docs/streaming-primary-event-grammar.md`.

Covered simulated provider behavior:

| Provider behavior                            | Canonical grammar outcome                                                                                                                                                               |
| -------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Streaming assistant text deltas              | `response_start`, text `content_block_start`, ordered `text_delta`, text `content_block_end`, `message_final`, optional `usage`, final `stop`.                                          |
| Streaming tool-call deltas by provider index | Tool-call `content_block_start`, ordered `tool_call_delta` carrying stable `ToolCallID`, final `content_block_end`, `message_final`, `tool_call_ready`, optional `usage`, final `stop`. |
| Usage metadata                               | Generic token counts in `usage`; request ID/provider/package diagnostics remain non-secret metadata.                                                                                    |
| Finish reasons                               | Provider raw finish reason retained only as diagnostics; canonical stop reason is normalized.                                                                                           |
| Setup failures before stream acceptance      | Ordinary Go error and no accepted-turn events.                                                                                                                                          |
| Accepted-turn provider failures              | Terminal `error`, optional `usage`, final `stop`, with no events after `stop`.                                                                                                          |

## Diagnostics Boundary

The proof keeps diagnostics observational and bounded:

| Retained diagnostic         | Boundary                                                               |
| --------------------------- | ---------------------------------------------------------------------- |
| Request ID                  | Non-secret provider correlation only.                                  |
| Provider/package identifier | `openai-compatible` and `github.com/jgabor/go-agent/providers/openai`. |
| HTTP status                 | Numeric provider response status when available.                       |
| Provider error type/code    | Provider-reported stable error classification when available.          |
| Raw stop reason             | Retained as diagnostic context, not a provider-specific event field.   |
| Sanitized excerpt           | Bounded non-secret excerpt after removing sensitive values.            |

The proof rejects diagnostic metadata that exposes credentials, authorization
headers, API keys, full prompts/messages, unredacted tool arguments,
environment values, credential-bearing URLs, pricing, registry, marketplace, or
Lira policy concepts. It does not allow opaque diagnostic maps.

## Grammar Finding

No provider-specific event field was required for the simulated
OpenAI-compatible behaviors. Provider wire facts such as choice deltas,
finish reasons, and stream chunks normalize into canonical event payloads plus
bounded diagnostics. The draft grammar does not need revision before Task 4.

## Provider Configuration Review

The current provider package remains a simple adapter configuration surface:
`Model`, `APIKey`, `BaseURL`, `HTTPClient`, and typed `ChatOptions`. It
implements the public `goagent.Model` streaming seam by sending Chat Completions
requests with `stream: true`, parsing SSE text/tool-call/usage/finish/error
records into canonical events, and rejecting non-SSE success responses instead
of hiding them behind a complete-response fallback. It still does not add custom
auth discovery, provider registry, provider selection, model marketplace
behavior, pricing, workdir behavior, or Lira product policy to core.
