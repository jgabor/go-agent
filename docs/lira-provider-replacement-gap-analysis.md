# Gap Analysis: Replacing Lira API and CLI Providers with go-agent

Date: 2026-05-07

## Executive Summary

`go-agent` is not ready to fully replace `~/git/lira`'s API and CLI provider implementation.

## Freshness Follow-up: Streaming-Primary Contract

After the streaming-primary runtime contract work, several gaps recorded below are no longer current `go-agent` runtime gaps:

- `Model` is now streaming-primary: providers implement `Stream(ctx, TurnRequest, emit func(Event)) error`.
- `Runner.Run`, `Runner.Stream`, and event sinks consume the same canonical event sequence; completion results are assembled from that stream.
- `Message` now has ordered `Blocks` for text, tool calls, and tool results while retaining `Content` as an ergonomic text projection.
- `ToolResult` now preserves either text content or JSON-compatible values through policy decisions, events, stream assembly, and sessions.
- `TurnResult`, `EventUsage`, and `RunResult.Usage` carry generic usage metadata. Cost and pricing remain Lira-owned product policy.
- `ModelFromSimple` keeps tests and local models concise without making the core turn-first again.

The replacement conclusion is unchanged: Lira should keep provider registry, credential discovery, auth/cache behavior, CLI subprocess providers, workflow policy, workdir behavior, pricing, and user-visible CLI semantics. The remaining generic `go-agent` gaps are now narrower: request-level model options, broader optional provider packages, provider SSE parsers beyond the proof harness, and any Lira-side adapter/parity tests.

The important distinction is ownership:

- `go-agent` is an embeddable Go agent runtime. It owns the loop, in-process tool execution, sessions, policy hooks, retry hooks, runtime events, and a provider-neutral `Model` interface.
- `lira` owns a product provider layer. It resolves configured providers, authenticates them, chooses provider-specific wire formats, streams provider responses, accounts for usage/cost, invokes host CLI subprocess providers, preserves CLI behavior, and integrates providers into Lira's workflow engine.

A full replacement would require `go-agent` to absorb multiple things it explicitly declares outside core scope: provider registry/credential discovery, auth assembly, CLI/product shell behavior, settings loading, host CLI subprocess integration, and workflow wiring. Those are not `go-agent` gaps. They are integration gaps that `lira` must keep owning if it embeds `go-agent`.

The feasible path is not replacement. It is layered adoption:

1. Keep Lira's provider config/auth/CLI adapter layer.
2. Add an adapter from Lira's provider contracts to `go-agent.Model` and from Lira's `toolkit.Toolkit` to `go-agent.Tool`.
3. Decide whether Lira wants `go-agent` to own the agent loop, or only use `go-agent` types/events behind existing Lira worker semantics.
4. Add missing `go-agent` runtime/provider features only where they are generic library capabilities, especially request-level model options, broader optional provider packages, and provider SSE parsers.

## Scope

This analysis compared:

- `go-agent` at `/home/jgabor/git/go-agent`.
- `lira` at `/home/jgabor/git/lira`.
- Lira provider/API implementation, CLI provider implementation, provider-facing CLI behavior, provider config/auth, and provider integration into worker execution.

This analysis does not propose porting Lira's workflow product, dashboard, MCP server, brainstorm flows, SQLite operational state, or team-mode orchestration into `go-agent`. Those are explicitly Lira product surfaces, not generic provider replacement requirements.

## Evidence: go-agent Current Surface

### Implemented in go-agent

`go-agent` currently provides these relevant runtime primitives:

| Capability                     | Status       | Evidence                                                                                                                                               |
| ------------------------------ | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Agent configuration            | Implemented  | `Agent` in `/home/jgabor/git/go-agent/api.go:9` carries instructions, model, tools, policy, sessions, event sinks, retry.                              |
| Runner API                     | Implemented  | `Runner.Run` and `Runner.Stream` in `/home/jgabor/git/go-agent/api.go:21`.                                                                             |
| Provider-neutral model seam    | Implemented  | `Model.Stream(context.Context, TurnRequest, func(Event)) error` in `/home/jgabor/git/go-agent/api.go`.                                                 |
| Turn request/result            | Implemented  | `TurnRequest` has instructions, messages, tools, session; `TurnResult` supports message, tool calls, stop reason, and usage for simple-model adapters. |
| Message roles and blocks       | Implemented  | system/user/assistant/tool roles plus ordered text/tool-call/tool-result blocks in `/home/jgabor/git/go-agent/api.go`.                                 |
| In-process tools               | Implemented  | `Tool`, `ToolCall`, JSON-capable `ToolResult`, and `ToolSchema` in `/home/jgabor/git/go-agent/api.go`.                                                 |
| Tool helpers                   | Implemented  | `NewTool`, `NewToolWithSchema`, `NewToolFromDefinition` in `/home/jgabor/git/go-agent/tool.go`.                                                        |
| Session abstraction            | Implemented  | `Session` and `SessionStore` in `/home/jgabor/git/go-agent/api.go:152`; memory store in `/home/jgabor/git/go-agent/session.go`.                        |
| Event stream                   | Implemented  | Canonical `Event` kinds, stream assembly, runtime stream implementation, and event-sink parity in root package tests.                                  |
| Policy hooks                   | Implemented  | `Policy`, `PolicyDecision`, and decision points in `/home/jgabor/git/go-agent/api.go:238`.                                                             |
| Retry hooks                    | Implemented  | `RetryPolicy`, `DecisionRetry`, and retry events in `/home/jgabor/git/go-agent/api.go`.                                                                |
| OpenAI-compatible chat adapter | Started      | `providers/openai.ChatModel` implements `goagent.Model`; it adapts chat completion responses into canonical events.                                    |
| Example CLI consumer           | Example only | `/home/jgabor/git/go-agent/examples/cli/main.go` uses a local fake model and is not a product CLI.                                                     |

### Explicit go-agent Non-Goals

`go-agent` explicitly does not own several things Lira's provider layer currently owns:

| Non-goal                                          | Evidence                                                                                               | Impact on replacement                                                                                                        |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------- |
| Provider registry or credential discovery         | `/home/jgabor/git/go-agent/README.md:150-153`                                                          | Lira cannot expect `go-agent` to resolve provider names, env vars, auth cache, token exchange, or fallback provider formats. |
| Auth assembly and approval UI                     | `/home/jgabor/git/go-agent/README.md:152-153`; `/home/jgabor/git/go-agent/.agentera/vision.yaml:44-49` | Lira must keep API key login/cache, Copilot token exchange, and host CLI auth checks.                                        |
| Settings, prompt, skill, or resource loading      | `/home/jgabor/git/go-agent/README.md:154`; `/home/jgabor/git/go-agent/.agentera/vision.yaml:44-49`     | Lira must keep provider/stage config and workflow prompt assembly.                                                           |
| CLI, TUI, package lifecycle, product shell        | `/home/jgabor/git/go-agent/README.md:155`; `/home/jgabor/git/go-agent/README.md:428`                   | Lira's command behavior is not a `go-agent` responsibility.                                                                  |
| MCP, sub-agent orchestration, workflow DSL wiring | `/home/jgabor/git/go-agent/README.md:156`; `/home/jgabor/git/go-agent/README.md:429-430`               | Lira must keep its workflow/router/MCP integration.                                                                          |
| Hosted platform/control plane                     | `/home/jgabor/git/go-agent/.agentera/vision.yaml:50-51`                                                | No Lira provider replacement impact except confirming library-first direction.                                               |
| Model marketplace                                 | `/home/jgabor/git/go-agent/.agentera/vision.yaml:55`                                                   | Provider catalog/aliases remain Lira-owned or external.                                                                      |
| Background shell abstraction                      | `/home/jgabor/git/go-agent/.agentera/vision.yaml:58`                                                   | CLI subprocess providers should not become core `go-agent`.                                                                  |

## Evidence: Lira Provider Surface

### Lira Driver Contract

Lira's provider layer is centered on `driver.Driver`:

```go
type Driver interface {
    Complete(ctx context.Context, req *Request) (*Response, error)
    StreamComplete(ctx context.Context, req *Request, fn StreamFunc) (*Response, error)
}
```

Evidence: `/home/jgabor/git/lira/internal/driver/types.go:10-15`.

Lira request fields are materially broader than `go-agent.TurnRequest`:

| Lira field                   | Purpose                                             | `go-agent` equivalent today                                                                      |
| ---------------------------- | --------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `System`                     | Provider system/developer instruction               | `TurnRequest.Instructions` exists.                                                               |
| `Messages []*schema.Message` | Multi-block transcript                              | `TurnRequest.Messages []goagent.Message` with ordered `Blocks` plus string `Content` projection. |
| `Tools *toolkit.Toolkit`     | Ordered JSON-schema tools returning `any`           | `[]ToolSpec` and `Tool` exist with JSON-capable `ToolResult`, but use different interfaces.      |
| `MaxTokens uint`             | Provider max output control                         | Missing.                                                                                         |
| `Temperature float64`        | Sampling control                                    | Missing.                                                                                         |
| `ReasoningEffort string`     | Provider reasoning control                          | Missing.                                                                                         |
| `Workdir string`             | CLI subprocess working directory                    | Non-goal for core; explicit Lira ownership.                                                      |
| `DisableTools bool`          | Force zero tools, including provider-internal tools | Missing. CLI-specific behavior is Lira-owned.                                                    |

Lira response fields are also broader:

| Lira field                | Purpose                                     | `go-agent` equivalent today                                                                                   |
| ------------------------- | ------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| `Message *schema.Message` | Assistant text/tool calls as content blocks | `TurnResult.Message` supports block content and `TurnResult.ToolCalls` remains available for simple adapters. |
| `Usage *schema.Usage`     | Input/output/cache token counts             | Generic `Usage` exists on turn results, events, and run results.                                              |
| `CostUSD float64`         | Cost accounting                             | Missing.                                                                                                      |

### Lira Message and Tool Schema

Lira's canonical schema supports content blocks:

- text blocks,
- tool call blocks,
- tool result blocks.

Evidence: `/home/jgabor/git/lira/internal/schema/schema.go:14-35`.

Lira tool calls and results carry raw JSON:

- `ToolCall.Input json.RawMessage`, evidence: `/home/jgabor/git/lira/internal/schema/schema.go:25-29`.
- `ToolResult.Content json.RawMessage`, evidence: `/home/jgabor/git/lira/internal/schema/schema.go:31-35`.

`go-agent` supports raw JSON tool input and JSON-compatible tool results. It is still not a drop-in replacement for Lira's toolkit interface because Lira tools return arbitrary `any` values through different package-owned contracts.

Lira's toolkit contract is:

```go
type Tool interface {
    Name() string
    Description() string
    Schema() (*jsonschema.Schema, error)
    Run(ctx context.Context, input json.RawMessage) (any, error)
}
```

Evidence: `/home/jgabor/git/lira/internal/toolkit/toolkit.go:11-16`.

`go-agent.Tool` is similar in spirit but not drop-in compatible:

```go
type Tool interface {
    Name() string
    Description() string
    Schema() ToolSchema
    Call(context.Context, ToolCall) (ToolResult, error)
}
```

Evidence: `/home/jgabor/git/go-agent/api.go:88-94`.

## Replacement Gap Matrix

### Critical go-agent Gaps

These are missing capabilities that are plausibly aligned with `go-agent`'s library/runtime/provider goals.

| Gap                                        | Severity                   | Why it matters                                                                                                                                                                                                                                                  | Evidence                                                                                                                                                                                                                                                                                                                                                                                                                        | Owner                                                                                                      |
| ------------------------------------------ | -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| Provider-level streaming contract          | Closed in go-agent runtime | Lira's worker calls `StreamComplete` as the main path, and API drivers parse provider SSE streams. `go-agent.Model` is now stream-first and runtime results assemble from canonical events. Provider-specific SSE parsers are still adapter/package work.       | Lira: `/home/jgabor/git/lira/internal/driver/types.go:12-15`, OpenAI SSE `/home/jgabor/git/lira/internal/driver/openai.go:210-280`, chat SSE `/home/jgabor/git/lira/internal/driver/chatcompletions.go:205-303`, Anthropic SSE `/home/jgabor/git/lira/internal/driver/anthropic.go:211-340`. go-agent: `Model.Stream` in `/home/jgabor/git/go-agent/api.go`; stream assembly in `/home/jgabor/git/go-agent/stream_assembly.go`. | `go-agent` owns the generic stream seam; provider packages or Lira own concrete wire parsers.              |
| Turn options for model parameters          | Critical                   | Lira depends on max tokens, temperature, and reasoning effort across API and CLI providers. `go-agent.TurnRequest` has no option bag or typed parameters.                                                                                                       | Lira request fields in `/home/jgabor/git/lira/internal/driver/types.go:17-30`; go-agent turn fields in `/home/jgabor/git/go-agent/api.go:50-56`.                                                                                                                                                                                                                                                                                | `go-agent` for generic model options; Lira for mapping provider/stage config.                              |
| Usage and cost metadata                    | Partially closed           | Lira accumulates token usage and cost across worker steps and exposes provider/accounting behavior. `go-agent` now carries generic usage, but not cost/pricing policy.                                                                                          | Lira response in `/home/jgabor/git/lira/internal/driver/types.go:32-36`; worker accumulation in `/home/jgabor/git/lira/internal/worker/worker.go`; go-agent `Usage`, `EventUsage`, and `RunResult.Usage` in `/home/jgabor/git/go-agent/api.go`.                                                                                                                                                                                 | `go-agent` for generic usage metadata; Lira for pricing rules and product accounting.                      |
| Rich message content blocks                | Closed in go-agent runtime | Lira represents mixed text/tool call/tool result content. `go-agent.Message` now supports ordered blocks, while provider-package mappings remain explicit adapter work.                                                                                         | Lira schema `/home/jgabor/git/lira/internal/schema/schema.go:14-35`; go-agent `Message.Blocks` and `Block` in `/home/jgabor/git/go-agent/api.go`.                                                                                                                                                                                                                                                                               | `go-agent` provides the generic block shape; Lira adapters must map product schema details.                |
| Raw JSON tool results                      | Closed in go-agent runtime | Lira tools return `any` and tool result content is raw JSON. `go-agent.ToolResult` now carries JSON-compatible content in addition to text content.                                                                                                             | Lira toolkit `/home/jgabor/git/lira/internal/toolkit/toolkit.go:11-16`; Lira tool result `/home/jgabor/git/lira/internal/schema/schema.go:31-35`; go-agent `ToolResult.JSON` in `/home/jgabor/git/go-agent/api.go`.                                                                                                                                                                                                             | `go-agent` preserves generic JSON result fidelity; Lira still needs toolkit adapters.                      |
| Disable-tools semantics                    | High                       | Lira can force zero tools, including provider-internal CLI tools. `go-agent` can pass no tools, but has no request-level `DisableTools` that communicates stronger intent.                                                                                      | Lira request field in `/home/jgabor/git/lira/internal/driver/types.go:25-29`; Claude adapter behavior in `/home/jgabor/git/lira/internal/driver/cli.go:530-537`.                                                                                                                                                                                                                                                                | `go-agent` for generic no-tools turn option; Lira for CLI-internal tool suppression.                       |
| Provider error classification and metadata | Medium                     | Lira maps driver, timeout, infra, tool, and stagnation failures into recovery categories. `go-agent` has stop reasons and retry events, but not Lira-compatible error codes/categories.                                                                         | Lira common errors `/home/jgabor/git/lira/internal/driver/errors.go`; worker errors `/home/jgabor/git/lira/internal/worker/errors.go`; recovery categories `/home/jgabor/git/lira/internal/recovery/recovery.go`. go-agent stop reasons `/home/jgabor/git/go-agent/api.go:218`.                                                                                                                                                 | Shared. `go-agent` can expose generic typed errors; Lira must map to product categories.                   |
| Provider adapter coverage                  | Medium                     | `go-agent` ships only OpenAI-compatible chat completions. Lira uses OpenAI Responses, chat completions, Anthropic, Copilot, Zen/OpenCode, Ollama/custom, and CLI providers.                                                                                     | go-agent OpenAI adapter `/home/jgabor/git/go-agent/providers/openai/openai.go`; Lira factory `/home/jgabor/git/lira/internal/driver/factory.go:15-32`.                                                                                                                                                                                                                                                                          | `go-agent` for optional generic provider packages; Lira for product-specific providers/token exchange/CLI. |
| Provider adapter streaming implementation  | Medium                     | The existing `go-agent` OpenAI adapter implements `Model.Stream` but still reads a complete chat completion response and adapts it into canonical events; it is not an SSE parser.                                                                              | go-agent OpenAI adapter in `/home/jgabor/git/go-agent/providers/openai/openai.go`; Lira SSE implementations listed above.                                                                                                                                                                                                                                                                                                       | `go-agent` if streaming provider adapters are in scope.                                                    |
| Provider HTTP option surface               | Medium                     | Lira provider code handles base URL path differences, custom headers, keyless auth, bearer/x-api-key differences, special model routing, and timeout differences. `go-agent.providers/openai.ChatModel` exposes only model, API key, base URL, and HTTP client. | go-agent `ChatModel` fields in `/home/jgabor/git/go-agent/providers/openai/openai.go:18-24`; Lira provider files under `/home/jgabor/git/lira/internal/driver`.                                                                                                                                                                                                                                                                 | `go-agent` for generic adapter options; Lira for provider-specific policy.                                 |

### Critical Lira Integration Gaps

These are required for replacement, but they should remain Lira-owned because they are product/provider orchestration or explicit `go-agent` non-goals.

| Gap                                    | Severity | Why it matters                                                                                                                                                                                                         | Evidence                                                                                                                                                                                                                                                        | Owner                                               |
| -------------------------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------- |
| Provider registry and selection        | Critical | Lira resolves `provider.default`, stage overrides, API vs CLI provider maps, utility models, and known provider formats. `go-agent` explicitly does not own provider registry or settings loading.                     | Lira provider docs `/home/jgabor/git/lira/docs/runtime/PROVIDERS.md:6-18`; config resolution `/home/jgabor/git/lira/internal/config/config.go:176-246`; go-agent non-goal `/home/jgabor/git/go-agent/README.md:150-156`.                                        | Lira.                                               |
| Provider config storage and validation | Critical | Lira persists provider config in its DB and validates API/CLI semantics. This is product state, not runtime library state.                                                                                             | Config structs `/home/jgabor/git/lira/internal/config/config.go:78-136`; config store `/home/jgabor/git/lira/internal/config/config_store.go`; go-agent settings non-goal `/home/jgabor/git/go-agent/README.md:154`.                                            | Lira.                                               |
| API key env/cache resolution           | Critical | Lira checks configured env vars, falls back to `$XDG_DATA_HOME/lira/auth.json`, supports keyless mode, and reports actionable auth errors. `go-agent` expects already-resolved credentials.                            | Provider auth contract `/home/jgabor/git/lira/docs/runtime/PROVIDERS.md:26-42`; factory auth lookup `/home/jgabor/git/lira/internal/driver/factory.go:107-126`; go-agent example passes `os.Getenv` from host in `/home/jgabor/git/go-agent/README.md:184-189`. | Lira.                                               |
| Copilot token exchange                 | Critical | Copilot is an explicit Lira API exception requiring GitHub OAuth cache, token exchange, proxy endpoint extraction, model policy enablement, and special headers. This is not a generic `go-agent` core concern.        | Lira docs `/home/jgabor/git/lira/docs/runtime/PROVIDERS.md:39-42`; implementation `/home/jgabor/git/lira/internal/auth/copilot.go`, `/home/jgabor/git/lira/internal/driver/copilot.go`.                                                                         | Lira.                                               |
| Host CLI provider auth readiness       | Critical | Lira checks host-owned auth homes/files for Claude, Codex, Gemini, OpenCode, and Pi without reading tokens. `go-agent` explicitly avoids background shell abstraction and product auth.                                | Lira docs `/home/jgabor/git/lira/docs/runtime/PROVIDERS.md:53-88`; host auth `/home/jgabor/git/lira/internal/auth/host_auth.go`; go-agent non-goals `/home/jgabor/git/go-agent/.agentera/vision.yaml:44-58`.                                                    | Lira.                                               |
| CLI subprocess adapters                | Critical | Lira invokes `pi`, `claude`, `codex`, `gemini`, and `opencode`, each with specific args, env, prompt transport, output parsing, usage/cost extraction, and redaction. `go-agent` should not own these as core runtime. | CLI adapter map `/home/jgabor/git/lira/internal/driver/cli.go:51-57`; go-agent background shell non-goal `/home/jgabor/git/go-agent/.agentera/vision.yaml:58`.                                                                                                  | Lira.                                               |
| Lira CLI command behavior              | High     | Commands like `lira run`, `auth`, `doctor`, `status`, `events`, `tasks`, and `brainstorm` expose user-visible contracts. `go-agent` has no product CLI and explicitly keeps CLI shell outside core.                    | Lira CLI `/home/jgabor/git/lira/internal/cli`; go-agent README non-goal `/home/jgabor/git/go-agent/README.md:155`; CLI roadmap deferred `/home/jgabor/git/go-agent/README.md:428`.                                                                              | Lira.                                               |
| Prompt/workflow assembly               | High     | Lira constructs system prompts from manifests, stage state, messages, and workflow context. `go-agent` accepts instructions/input but does not load prompts, skills, resources, or workflows.                          | Lira worker `/home/jgabor/git/lira/internal/worker/worker.go`; go-agent non-goal `/home/jgabor/git/go-agent/README.md:154-156`.                                                                                                                                 | Lira.                                               |
| Lira recovery policy and budgets       | High     | Lira has stage timeouts, max steps, max output tokens, max tool calls, retry recipes, and error category mapping. `go-agent` has max steps and retry primitives, but not Lira's product-level recovery semantics.      | Lira worker and recovery files `/home/jgabor/git/lira/internal/worker/worker.go`, `/home/jgabor/git/lira/internal/recovery/recovery.go`; go-agent retry `/home/jgabor/git/go-agent/api.go:285`.                                                                 | Lira, with possible mapping to go-agent primitives. |
| Diagnostics and redaction policy       | High     | Lira scrubs token-looking text from CLI stderr/stdout/logs and separates API-vs-CLI auth ownership in doctor/status. This is product security behavior.                                                                | Lira docs `/home/jgabor/git/lira/docs/runtime/PROVIDERS.md:86-98`; redaction `/home/jgabor/git/lira/internal/auth/host_auth.go:219-224`; logging `/home/jgabor/git/lira/internal/driver/logging.go`.                                                            | Lira.                                               |
| Provider-specific model routing        | Medium   | Lira routes Zen and Copilot requests across OpenAI Responses, Chat Completions, and Anthropic based on provider/model. This is Lira provider policy.                                                                   | Factory `/home/jgabor/git/lira/internal/driver/factory.go:15-32`; Zen `/home/jgabor/git/lira/internal/driver/zen.go`; Copilot `/home/jgabor/git/lira/internal/driver/copilot.go`.                                                                               | Lira.                                               |

## Detailed API Provider Gaps

### OpenAI Responses API

Lira has an OpenAI Responses driver with behavior not present in `go-agent`:

| Behavior                                                                     | Lira evidence                                                                 | go-agent status                                        |
| ---------------------------------------------------------------------------- | ----------------------------------------------------------------------------- | ------------------------------------------------------ |
| `/v1/responses` endpoint construction                                        | `/home/jgabor/git/lira/internal/driver/openai.go:282-300`                     | Missing. Existing adapter targets `/chat/completions`. |
| `stream: true` support                                                       | `/home/jgabor/git/lira/internal/driver/openai.go:124-130`                     | Missing.                                               |
| SSE event handling for `response.output_text.delta`, completion, and failure | `/home/jgabor/git/lira/internal/driver/openai.go:210-280`                     | Missing.                                               |
| Responses `input` item mapping                                               | `/home/jgabor/git/lira/internal/driver/openai.go`                             | Missing.                                               |
| Responses `function_call` and `function_call_output` mapping                 | `/home/jgabor/git/lira/internal/driver/openai.go:341-405`                     | Missing.                                               |
| Codex backend defaults and special cases                                     | `/home/jgabor/git/lira/internal/driver/openai.go:18-22`, `114-116`, `143-163` | Lira-specific provider policy.                         |
| Custom headers after base headers                                            | `/home/jgabor/git/lira/internal/driver/openai.go:190-192`                     | Missing in `go-agent` OpenAI adapter.                  |

Classification:

- Generic OpenAI Responses adapter support could be a `go-agent` provider-package gap.
- Codex-specific backend behavior and Lira config/auth wiring are Lira gaps/non-goals for `go-agent`.

### Chat Completions Providers

Lira's chat-completions driver covers OpenRouter, Ollama, custom providers, Zen fallback, and Copilot non-Claude models.

| Behavior                                        | Lira evidence                                                                          | go-agent status                                                                                                |
| ----------------------------------------------- | -------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Streaming `stream: true` with `[DONE]` parsing  | `/home/jgabor/git/lira/internal/driver/chatcompletions.go:147-153`, `205-303`          | Missing.                                                                                                       |
| Incremental tool argument accumulation by index | `/home/jgabor/git/lira/internal/driver/chatcompletions.go:223-294`                     | Missing.                                                                                                       |
| Final usage capture                             | `/home/jgabor/git/lira/internal/driver/chatcompletions.go:326-343`                     | Missing.                                                                                                       |
| `reasoning_effort` request field                | `/home/jgabor/git/lira/internal/driver/chatcompletions.go:38-47`                       | Missing.                                                                                                       |
| Provider-specific URL construction              | `/home/jgabor/git/lira/internal/driver/chatcompletions.go:311-312` and factory routing | Partially present; `go-agent` adapter has `BaseURL` plus `/chat/completions`, but not Lira's provider routing. |

Classification:

- Streaming, usage, and common model options are `go-agent` provider/runtime gaps.
- OpenRouter/Ollama/custom provider selection and config are Lira gaps/non-goals for `go-agent`.

### Anthropic Messages API

Lira has Anthropic support used indirectly by Zen and Copilot.

| Behavior                                | Lira evidence                                                                                                               | go-agent status                                 |
| --------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| `/v1/messages` request/response mapping | `/home/jgabor/git/lira/internal/driver/anthropic.go`                                                                        | Missing.                                        |
| Anthropic streaming event parser        | `/home/jgabor/git/lira/internal/driver/anthropic.go:211-340`                                                                | Missing.                                        |
| Anthropic content block mapping         | `/home/jgabor/git/lira/internal/driver/anthropic.go:393-464`                                                                | Not directly supported by `go-agent.Message`.   |
| `x-api-key` vs bearer auth mode         | `/home/jgabor/git/lira/internal/driver/anthropic.go:183-190`; Copilot in `/home/jgabor/git/lira/internal/driver/copilot.go` | Missing as adapter capability.                  |
| Default max tokens of `65536`           | `/home/jgabor/git/lira/internal/driver/anthropic.go:162-166`                                                                | Missing; provider policy may remain in adapter. |

Classification:

- A generic Anthropic adapter is a possible `go-agent` provider-package gap.
- Zen/Copilot routing and auth exceptions are Lira-owned.

### Copilot API Provider

Copilot support is not a generic provider adapter. It combines GitHub auth, token exchange, dynamic API endpoints, model policy enablement, and special request headers.

Required Lira behavior:

- `auth_mode="token_exchange"` allowed only for `copilot`.
- Load cached GitHub OAuth credentials from Lira auth cache.
- Exchange token through GitHub/Copilot endpoint.
- Extract `proxy-ep` to derive provider base URL.
- Enable model policy before use.
- Route Claude models through Anthropic-style behavior and other models through chat completions.

Evidence: `/home/jgabor/git/lira/internal/auth/copilot.go`, `/home/jgabor/git/lira/internal/driver/copilot.go`, `/home/jgabor/git/lira/internal/config/config.go:559-565`.

Classification: Lira gap. This should not move into `go-agent` core. At most, Lira can implement a `go-agent.Model` adapter backed by its existing Copilot driver.

### OpenCode Zen API

Zen behavior includes provider-specific base URL defaults and model routing:

- default base URL `https://opencode.ai`,
- `gpt-*`, `o1*`, `o3*` routed to OpenAI Responses,
- `claude-*` routed to Anthropic,
- everything else routed to Chat Completions,
- known Anthropic streaming limitation through Zen.

Evidence: `/home/jgabor/git/lira/internal/driver/zen.go`.

Classification: Lira gap. This is a product/provider policy adapter, not `go-agent` core.

## Detailed CLI Provider Gaps

Lira's CLI providers execute host-owned external agent CLIs. These are not equivalent to `go-agent` tools or provider adapters.

### Common CLI Driver Behavior

Lira CLI provider behavior includes:

- provider names `pi`, `claude`, `codex`, `gemini`, `opencode`, evidence: `/home/jgabor/git/lira/internal/driver/cli.go:51-57`,
- `HandlesToolsInternally() == true`, evidence: `/home/jgabor/git/lira/internal/driver/cli.go:109`,
- ignores Lira toolkit and max tokens with warnings, evidence: `/home/jgabor/git/lira/internal/driver/cli.go:116-123`,
- preflights host auth readiness, evidence: `/home/jgabor/git/lira/internal/driver/cli.go:124-130`,
- uses request workdir over configured workdir, evidence: `/home/jgabor/git/lira/internal/driver/cli.go:158-161`,
- redacts subprocess stderr and parsed output, evidence: `/home/jgabor/git/lira/internal/driver/cli.go:173-210`,
- implements `StreamComplete` as `Complete` passthrough, evidence: `/home/jgabor/git/lira/internal/driver/cli.go:269-271`.

Classification: Lira gap/non-goal for `go-agent`. `go-agent` explicitly rejects a background shell abstraction; execution belongs to explicit tools.

### Pi CLI

Required behavior:

- binary `pi`,
- args `-p --mode json`, optional `--model`, optional `--thinking`,
- prompt on stdin,
- parses JSONL `message_end` usage and `agent_end` final answer,
- extracts cost from scalar or object.

Evidence: `/home/jgabor/git/lira/internal/driver/cli.go:287-491`.

Classification: Lira gap. This is a concrete host CLI adapter.

### Claude CLI

Required behavior:

- binary `claude`,
- args `-p --output-format json`,
- workdir handling via `--permission-mode bypassPermissions --add-dir`,
- fallback `--dangerously-skip-permissions`,
- tool suppression through `--tools ""` and `--strict-mcp-config`,
- model and effort flags,
- `CLAUDECODE` environment scrubbing,
- optional OTLP telemetry receiver and env injection,
- JSON output parsing with usage/cost merging.

Evidence: `/home/jgabor/git/lira/internal/driver/cli.go:494-586` and OTLP behavior in `/home/jgabor/git/lira/internal/driver/cli.go:132-151`.

Classification: Lira gap. This should remain outside `go-agent` core.

### Codex CLI

Required behavior:

- binary `codex`,
- args `exec --json --skip-git-repo-check`, optional `--cd`, optional `--model`, optional `-c model_reasoning_effort=...`, final `-`,
- prompt on stdin,
- JSONL/JSON output parsing across multiple possible fields.

Evidence: `/home/jgabor/git/lira/internal/driver/cli.go:588-666` and shared parsing in `/home/jgabor/git/lira/internal/driver/cli.go:765-799`.

Classification: Lira gap.

### OpenCode CLI

Required behavior:

- binary `opencode`,
- args `run --format json --dangerously-skip-permissions`, optional `--model`, optional `--dir`, optional `--variant`, prompt as final arg,
- JSONL parsing for `text`, `step_finish`, and `error`,
- token/cost accumulation.

Evidence: `/home/jgabor/git/lira/internal/driver/cli.go:667-763`.

Classification: Lira gap.

### Gemini CLI

Required behavior:

- binary `gemini`,
- args `-p <prompt> --output-format json`, optional `--model`,
- no stdin,
- JSON response/error parsing,
- token usage summation across `stats.models`.

Evidence: `/home/jgabor/git/lira/internal/driver/cli.go:801-880`.

Classification: Lira gap.

## Detailed Config and Auth Gaps

### Provider Config

Lira's provider config supports:

- API provider fields: `model`, `model_utility`, `auth_mode`, `base_url`, `api_key_env`, `headers`, `reasoning_effort`, evidence: `/home/jgabor/git/lira/internal/config/config.go:78-95`.
- CLI provider fields: `model`, `model_utility`, `path`, `host_auth`, `reasoning_effort`, evidence: `/home/jgabor/git/lira/internal/config/config.go:105-118`.
- default provider, stage provider override, stage model override, stage reasoning effort override, utility model resolution, evidence: `/home/jgabor/git/lira/internal/config/config.go:176-246`, `/home/jgabor/git/lira/internal/config/config.go:349-364`.

`go-agent` has no config loader and should not grow one in core. The closest surfaces are direct Go construction via `goagent.New(...)` and `openai.ChatModel{...}`.

Classification: Lira gap. Lira needs an adapter that reads Lira config and builds either Lira drivers, `go-agent.Model` implementations, or `go-agent.Agent` values.

### API Auth

Lira's API auth modes are:

- `api_key`,
- `keyless`,
- `token_exchange`.

Evidence: `/home/jgabor/git/lira/internal/config/config.go:23-26`; docs in `/home/jgabor/git/lira/docs/runtime/PROVIDERS.md:26-42`.

`go-agent.providers/openai.ChatModel` requires an already populated `APIKey` and does not discover env vars or cache credentials.

Classification: Lira gap. This is an explicit `go-agent` non-goal.

### Host CLI Auth

Lira must preserve host auth defaults:

| Provider | Home variable         | Default home     | Auth readiness path                                            |
| -------- | --------------------- | ---------------- | -------------------------------------------------------------- |
| Claude   | `CLAUDE_CONFIG_DIR`   | `~/.claude`      | `.credentials.json` on Linux/Windows; macOS Keychain exception |
| Codex    | `CODEX_HOME`          | `~/.codex`       | `auth.json`                                                    |
| Gemini   | `GEMINI_CLI_HOME`     | `~/.gemini`      | `oauth_creds.json`                                             |
| OpenCode | `XDG_DATA_HOME`       | `~/.local/share` | `opencode/auth.json`                                           |
| Pi       | `PI_CODING_AGENT_DIR` | `~/.pi/agent`    | `auth.json`                                                    |

Evidence: `/home/jgabor/git/lira/docs/runtime/PROVIDERS.md:75-81`; implementation in `/home/jgabor/git/lira/internal/auth/host_auth.go`.

Classification: Lira gap. `go-agent` should not import, read, or validate host CLI credentials.

## Worker and Runtime Integration Gaps

Lira's worker uses providers inside a larger workflow loop:

- builds `driver.Request` from manifest system prompt, accumulated messages, toolkit, reasoning effort, and workdir,
- calls `StreamComplete` as the main provider path,
- extracts tool calls,
- runs toolkit tools,
- appends tool result messages,
- tracks token usage and cost,
- enforces timeout, max steps, max output tokens, and max tool calls.

Evidence: `/home/jgabor/git/lira/internal/worker/worker.go` and `/home/jgabor/git/lira/internal/driver/types.go`.

`go-agent` duplicates part of this loop: it calls a model, executes in-process tools, appends tool results to session, emits events, applies policy, and stops on step limit. That overlap is useful but not a drop-in replacement because:

- Lira and `go-agent` are both stream-first at the model boundary, but their request/response and provider-policy ownership differ.
- Lira's tools return arbitrary `any` values through toolkit contracts; `go-agent` tool results preserve text or JSON-compatible values through a different interface.
- Lira's worker owns product budgets and recovery categories; `go-agent` owns generic retry and stop reasons.
- Lira's CLI providers handle tools internally and must bypass Lira's toolkit differently than API providers; `go-agent` assumes tools are explicit in-process Go capabilities.
- Lira's workflow transcript and event persistence are product state; `go-agent.Session` is an in-memory/default abstraction with host-provided storage.

Classification: mixed.

- Generic runtime overlap can move toward `go-agent` if Lira deliberately adopts its loop.
- Lira-specific workflow state, recovery, budgets, and CLI provider bypass semantics remain Lira gaps.

## User-Visible CLI Behavior That Must Stay in Lira

`go-agent` has no installable product CLI. The only CLI is an example using a fake local model. Lira's CLI has user-visible behavior that must remain Lira-owned:

- top-level binary and command taxonomy under `lira`, evidence: `/home/jgabor/git/lira/internal/cli/cli.go:21-79`,
- global `--in-memory`,
- `lira run` flags and JSON input contract, evidence: `/home/jgabor/git/lira/internal/cli/run.go:216-400`,
- TTY Bubble Tea display and non-TTY event output, evidence: `/home/jgabor/git/lira/internal/cli/run.go:32-214`,
- `auth login/status/logout` API-vs-CLI semantics, evidence: `/home/jgabor/git/lira/internal/cli/auth_cmd.go`,
- `doctor` provider/auth checks, evidence: `/home/jgabor/git/lira/internal/doctor/doctor.go`,
- `status`, `events`, `tasks` format and `--fields` behavior, evidence: `/home/jgabor/git/lira/internal/cli/status.go`, `/home/jgabor/git/lira/internal/cli/events_cmd.go`, `/home/jgabor/git/lira/internal/cli/tasks.go`,
- brainstorm provider use and interactive flows, evidence: `/home/jgabor/git/lira/internal/cli/brainstorm.go`.

Classification: Lira gap/non-goal for `go-agent`.

## Minimal Adapter Architecture for Partial Adoption

A realistic integration avoids replacing Lira's provider layer all at once.

### Option A: Wrap Lira Drivers as go-agent Models

Implement a Lira-side adapter:

```go
type DriverModel struct {
    Driver driver.Driver
    Options DriverModelOptions
}

func (m DriverModel) Stream(ctx context.Context, req goagent.TurnRequest, emit func(goagent.Event)) error {
    // Convert go-agent messages/tools/options to driver.Request.
    // Call StreamComplete and normalize provider chunks into go-agent events.
    // Emit canonical events; completion callers assemble results from the stream.
}
```

Benefits:

- Preserves Lira provider config/auth/CLI adapters.
- Lets Lira use `go-agent.Runner` for the loop if desired.
- Avoids forcing Copilot, host CLI auth, or provider registry into `go-agent`.

Problems:

- Must normalize Lira provider chunks into canonical `go-agent.Event` values.
- Preserves generic usage through `go-agent.Usage`; Lira still owns cost/pricing accounting.
- Requires conversion between Lira content blocks and `go-agent.Message` blocks.
- Requires conversion between `toolkit.Toolkit` and `go-agent.Tool`.

### Option B: Use go-agent Types Only at Provider Boundary

Keep Lira worker loop and provider layer, but use `go-agent` abstractions for generic model/tool/event concepts where beneficial.

Benefits:

- Lower risk.
- Preserves Lira's stream-first worker behavior.
- Avoids mismatch between Lira budgets/recovery and `go-agent.Runner`.

Problems:

- Less actual replacement.
- Lira keeps duplicate loop concepts.
- Requires careful naming so `go-agent` does not become a cosmetic dependency.

### Option C: Port Generic API Providers to go-agent Provider Packages

Move generic provider implementations into optional `go-agent/providers/...` packages:

- OpenAI Chat Completions streaming,
- OpenAI Responses,
- Anthropic Messages,
- maybe generic OpenAI-compatible custom base URL support.

Keep in Lira:

- provider registry/config,
- auth cache/env resolution,
- Copilot token exchange,
- Zen routing,
- CLI subprocess providers,
- product CLI behavior,
- workflow budgets/recovery.

Benefits:

- Strengthens `go-agent` as a reusable library.
- Reduces duplicate provider wire-format code across future Go apps.

Problems:

- Still requires `go-agent` request option work and provider-package SSE parser work before provider packages can be faithful across Lira's full API surface.
- Still does not produce a full replacement of Lira's provider layer.

## Prioritized Work Required Before Replacement

### Required in go-agent

1. Add a generic turn options mechanism for max output tokens, temperature, reasoning effort, provider metadata, and disable-tools intent if the core needs request-level knobs.
2. Extend optional provider packages only where the core data model can represent their behavior faithfully.
3. Add concrete provider SSE parser tests where provider packages support token/tool-call streaming.
4. Keep pricing, provider registry, credential discovery, workdir behavior, and Lira workflow policy outside `go-agent` core.

### Required in Lira

1. Keep provider config, provider selection, auth, and CLI subprocess adapters outside `go-agent`.
2. Build explicit adapters between Lira `driver.Driver` and `go-agent.Model` if embedding `go-agent.Runner`.
3. Build explicit adapters between Lira `toolkit.Toolkit` and `go-agent.Tool`.
4. Preserve Lira's API-vs-CLI provider ownership contract from `docs/runtime/PROVIDERS.md`.
5. Preserve auth cache, env lookup, keyless mode, Copilot token exchange, and host CLI auth readiness behavior.
6. Preserve CLI provider command args, env handling, workdir behavior, output parsers, telemetry, usage/cost extraction, and redaction.
7. Preserve Lira CLI user-visible contracts and workflow event/output semantics.
8. Decide where token/cost accounting lives if `go-agent` becomes the loop owner.
9. Decide where Lira recovery categories map to `go-agent` stop reasons and errors.
10. Add integration tests that run existing Lira provider tests through the adapter layer before deleting any Lira provider implementation.

## Decision: Replacement Readiness

Current readiness: **not ready**.

`go-agent` still cannot replace Lira's API/CLI provider implementation wholesale today. It can now standardize more of the generic inner runtime stream and block-message contract, and it can host optional generic provider adapters in the future. The following Lira provider responsibilities should not move into `go-agent` core because they are explicit non-goals:

- provider registry and product config resolution,
- credential discovery and auth cache,
- auth login/status/logout behavior,
- Copilot token exchange,
- host CLI auth readiness,
- CLI subprocess provider adapters,
- Lira command UX,
- workflow orchestration and recovery policy,
- MCP/dashboard/brainstorm/team-mode integration.

The most accurate target is: `go-agent` becomes a reusable runtime and possibly a source of generic API provider adapters; Lira remains the owner of provider policy, auth, CLI subprocess integration, and product orchestration.

## Files Inspected

### go-agent

- `/home/jgabor/git/go-agent/AGENTS.md`
- `/home/jgabor/git/go-agent/README.md`
- `/home/jgabor/git/go-agent/TODO.md`
- `/home/jgabor/git/go-agent/.agentera/vision.yaml`
- `/home/jgabor/git/go-agent/.agentera/plan.yaml`
- `/home/jgabor/git/go-agent/.agentera/progress.yaml`
- `/home/jgabor/git/go-agent/api.go`
- `/home/jgabor/git/go-agent/doc.go`
- `/home/jgabor/git/go-agent/facade.go`
- `/home/jgabor/git/go-agent/runner.go`
- `/home/jgabor/git/go-agent/session.go`
- `/home/jgabor/git/go-agent/tool.go`
- `/home/jgabor/git/go-agent/tool_execution.go`
- `/home/jgabor/git/go-agent/providers/openai/openai.go`
- `/home/jgabor/git/go-agent/providers/openai/openai_test.go`
- `/home/jgabor/git/go-agent/runner_test.go`
- `/home/jgabor/git/go-agent/retry_test.go`
- `/home/jgabor/git/go-agent/session_test.go`
- `/home/jgabor/git/go-agent/stream_test.go`
- `/home/jgabor/git/go-agent/tool_test.go`
- `/home/jgabor/git/go-agent/facade_test.go`
- `/home/jgabor/git/go-agent/examples/cli/main.go`
- `/home/jgabor/git/go-agent/examples/cli/README.md`
- `/home/jgabor/git/go-agent/examples/minimal/main.go`
- `/home/jgabor/git/go-agent/examples/service/main.go`
- `/home/jgabor/git/go-agent/examples/worker/main.go`

### lira

- `/home/jgabor/git/lira/README.md`
- `/home/jgabor/git/lira/docs/PRD.md`
- `/home/jgabor/git/lira/docs/runtime/PROVIDERS.md`
- `/home/jgabor/git/lira/docs/runtime/OVERVIEW.md`
- `/home/jgabor/git/lira/artifacts/plan/gemini_cli_provider.md`
- `/home/jgabor/git/lira/cmd/lira/main.go`
- `/home/jgabor/git/lira/internal/auth/auth.go`
- `/home/jgabor/git/lira/internal/auth/copilot.go`
- `/home/jgabor/git/lira/internal/auth/github.go`
- `/home/jgabor/git/lira/internal/auth/host_auth.go`
- `/home/jgabor/git/lira/internal/cli/auth_cmd.go`
- `/home/jgabor/git/lira/internal/cli/brainstorm.go`
- `/home/jgabor/git/lira/internal/cli/chat.go`
- `/home/jgabor/git/lira/internal/cli/cli.go`
- `/home/jgabor/git/lira/internal/cli/config_cmd.go`
- `/home/jgabor/git/lira/internal/cli/config_wizard.go`
- `/home/jgabor/git/lira/internal/cli/doctor.go`
- `/home/jgabor/git/lira/internal/cli/dry_run.go`
- `/home/jgabor/git/lira/internal/cli/engine_setup.go`
- `/home/jgabor/git/lira/internal/cli/events_cmd.go`
- `/home/jgabor/git/lira/internal/cli/fields.go`
- `/home/jgabor/git/lira/internal/cli/input_json.go`
- `/home/jgabor/git/lira/internal/cli/mcp.go`
- `/home/jgabor/git/lira/internal/cli/ndjson.go`
- `/home/jgabor/git/lira/internal/cli/personality_generate_cmd.go`
- `/home/jgabor/git/lira/internal/cli/run.go`
- `/home/jgabor/git/lira/internal/cli/status.go`
- `/home/jgabor/git/lira/internal/cli/styles.go`
- `/home/jgabor/git/lira/internal/cli/tasks.go`
- `/home/jgabor/git/lira/internal/cli/views.go`
- `/home/jgabor/git/lira/internal/cli/workflow_actions.go`
- `/home/jgabor/git/lira/internal/config/config.go`
- `/home/jgabor/git/lira/internal/config/config_store.go`
- `/home/jgabor/git/lira/internal/conductor/conductor.go`
- `/home/jgabor/git/lira/internal/doctor/doctor.go`
- `/home/jgabor/git/lira/internal/driver/anthropic.go`
- `/home/jgabor/git/lira/internal/driver/chatcompletions.go`
- `/home/jgabor/git/lira/internal/driver/cli.go`
- `/home/jgabor/git/lira/internal/driver/copilot.go`
- `/home/jgabor/git/lira/internal/driver/errors.go`
- `/home/jgabor/git/lira/internal/driver/factory.go`
- `/home/jgabor/git/lira/internal/driver/http_error.go`
- `/home/jgabor/git/lira/internal/driver/instrumented.go`
- `/home/jgabor/git/lira/internal/driver/logging.go`
- `/home/jgabor/git/lira/internal/driver/openai.go`
- `/home/jgabor/git/lira/internal/driver/types.go`
- `/home/jgabor/git/lira/internal/driver/zen.go`
- `/home/jgabor/git/lira/internal/log/log.go`
- `/home/jgabor/git/lira/internal/recovery/recovery.go`
- `/home/jgabor/git/lira/internal/schema/schema.go`
- `/home/jgabor/git/lira/internal/toolkit/toolkit.go`
- `/home/jgabor/git/lira/internal/worker/errors.go`
- `/home/jgabor/git/lira/internal/worker/worker.go`
