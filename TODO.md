# TODO

Source of truth for product/runtime capability status: `README.md` Features &
Roadmap table. This backlog breaks that roadmap into executable slices and may
also track repository bookkeeping that should not appear in the README roadmap.

## Now

- [ ] Preserve provider reasoning fields across OpenAI-compatible tool-call loops.
  - Trigger: Aila config smoke passes non-tool and streaming calls against OpenAI-compatible DeepSeek models, but tool calls fail with DeepSeek `400`: `The reasoning_content in the thinking mode must be passed back to the API.`
  - Current behavior: `providers/openai.ChatModel` maps assistant/tool-call messages through the common go-agent event/session shape, but provider-specific assistant fields such as `reasoning_content` are not preserved and replayed when a tool result is sent back to the provider.
  - Desired behavior: OpenAI-compatible providers that require reasoning replay during tool-call loops can round-trip `assistant.reasoning_content` without exposing it as normal assistant text, leaking it into unsafe diagnostics, or forcing hosts to use deprecated non-thinking aliases.
  - Secondary follow-up: consider typed provider options for thinking controls such as `thinking: {"type":"disabled"}`, but do not make thinking toggles the prerequisite for correct replay semantics.
  - Tests: add provider adapter coverage for streaming/non-streaming `reasoning_content`, assistant/tool replay that includes required reasoning fields, a failing-provider fixture matching the DeepSeek `400`, successful tool-call replay when reasoning is preserved, and diagnostics that do not expose API keys or raw hidden reasoning beyond bounded test fixtures.

## Next

## Later

- [ ] Add recoverable-denial parity for `DecisionToolResult` policy decisions.
  - Trigger: Aila needs post-execution tool-result policy recovery, such as secret redaction, unsafe output blocking, or replacing a real tool result with a synthetic denial/defer result returned to the model.
  - Current behavior: `PolicyDecision.ToolResult` is honored only when `Allowed == false` for `DecisionToolCall`; the `DecisionToolResult` branch in `runner.callTool` still stops with `StopPolicyDenied` whenever `Allowed == false`.
  - Desired behavior: when policy denies `DecisionToolResult` with non-nil `PolicyDecision.ToolResult`, validate the synthetic result, enforce cumulative output limits, append the synthetic tool-role message, emit the usual `EventPolicyDecision` and `EventToolResult`, increment tool-call/output accounting consistently, and continue the run. Denial without a synthetic result must keep terminating with `StopPolicyDenied`.
  - Tests: add coverage proving `DecisionToolResult` recoverable denial reaches the next model turn, suppresses the original blocked result from session/events, preserves hard-denial behavior, and observes output/tool-call limits like `DecisionToolCall` synthetic denials.
- [ ] Add a provider adapter package beyond OpenAI Chat Completions.
  - Trigger: a concrete host need and provider-specific plan exists for Anthropic, OpenAI Realtime, plan/device-code APIs, or another non-Chat-Completions integration.
  - Constraint: keep the core runtime provider-agnostic; adapter work belongs in a focused provider package or host-owned package, not in the root runtime contract.

## Done

- [x] Publish product-facing README and roadmap.
- [x] Initialize Go module as `github.com/jgabor/go-agent`.
- [x] Establish DX baseline with formatting, linting, local hooks, and Mage gates.
- [x] Add CI for the canonical `mage check` gate.
- [x] Lock the first-slice public API contract.
- [x] Specify runtime behavior with tests before broad implementation.
- [x] Implement minimal function tools for the README quick start.
- [x] Implement the core agent loop.
- [x] Implement minimal policy hooks in the core loop.
- [x] Implement structured streaming events.
- [x] Expand tool schema support.
- [x] Add pluggable session storage.
- [x] Expand policy hooks.
- [x] Add an OpenAI-compatible provider adapter.
- [x] Add observability integration points.
- [x] Add a minimal app example.
- [x] Add a service example.
- [x] Add a worker example.
- [x] Add a CLI example.
- [x] Add a factory-first constructor facade for runtime defaults.
- [x] Design runtime retry defaults through typed policy decisions.
- [x] Implement observable model and runtime retry.
- [x] Add a rich `ToolDefinition` path for advanced tools.
- [x] Add policy-governed tool retry for retry-safe tools.
- [x] Complete runtime ergonomics plan freshness checkpoint.
- [x] Complete runtime depth and test locality plan freshness checkpoint.
- [x] Complete streaming-primary runtime contract freshness checkpoint.
- [x] Resolve Task 2 typed-usage blocker.
- [x] Complete Chat Completions Streaming Fidelity freshness checkpoint.
- [x] Complete Aila-facing runtime features: run overrides and limits, JSON replay and correlation, policy pending and tool-call recoverable denials, rich tool results, streaming tool progress, optional model capability hints.
- [x] Complete Aila-facing runtime follow-ups: provider capability facts, policy cancellation classification, and documentation alignment.
