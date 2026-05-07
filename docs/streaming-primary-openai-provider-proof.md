# OpenAI-Compatible Provider Grammar Proof

Date: 2026-05-07

This document records the Task 3 proof from the Streaming-Primary Runtime
Contract plan. At the time it was written, the proof was a pre-migration
artifact only and did not itself migrate the runtime or
`providers/openai.ChatModel` to the streaming-primary seam.

Repository reality has since moved on: Task 5 migrated the public model seam to
`Model.Stream(ctx, TurnRequest, emit) error`, and the current
`providers/openai.ChatModel` implements `goagent.Model` through `ChatModel.Stream`.
The proof below remains historical evidence that OpenAI-compatible provider
behavior fit the canonical grammar before that migration.

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
| Raw stop reason             | Retained as diagnostic context, not a provider-specific event field.   |
| Opaque metadata             | Allowed only when non-secret and not product policy.                   |

The proof rejects diagnostic metadata that exposes credentials, authorization
headers, API keys, pricing, registry, marketplace, or Lira policy concepts.

## Grammar Finding

No provider-specific event field was required for the simulated
OpenAI-compatible behaviors. Provider wire facts such as choice deltas,
finish reasons, and stream chunks normalize into canonical event payloads plus
bounded diagnostics. The draft grammar does not need revision before Task 4.

## Provider Configuration Review

The current provider package remains a simple adapter configuration surface:
`Model`, `APIKey`, `BaseURL`, and `HTTPClient`. It implements the public
`goagent.Model` streaming seam without adding custom auth discovery, provider
registry, provider selection, model marketplace behavior, pricing, workdir
behavior, or Lira product policy to core.
