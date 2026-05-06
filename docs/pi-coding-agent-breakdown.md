# Pi Coding Agent Breakdown

Pi's strongest idea is not a novel agent loop. It is a compact orchestration shell around `@mariozechner/pi-agent-core`, with sessions, resources, extensions, tools, models, and UIs all routed through one `AgentSession` abstraction.

Scope note: I analyzed `packages/coding-agent` from `https://github.com/badlogic/pi-mono/tree/main/packages/coding-agent`. The actual LLM loop and provider primitives live partly in sibling packages like `@mariozechner/pi-agent-core`, `@mariozechner/pi-ai`, and `@mariozechner/pi-tui`.

## TL;DR

Pi is a mode-agnostic coding-agent harness: one core runtime feeds an interactive TUI, print/JSON mode, RPC mode, and SDK embedding. Its most transferable primitives are the session tree, resource loader, extension event bus, definition-first tool registry, and cwd-bound runtime replacement model.

For `go-agent`, Pi mostly validates the current architecture: compact core, strong events, host-owned policy, and optional outer shells. The parts to avoid are the runtime-loaded extension marketplace, TUI-coupled tool rendering, and product-level resource/package machinery in the root runtime.

## Source Overview

The README describes Pi as a minimal terminal coding harness that adapts through TypeScript extensions, skills, prompt templates, themes, and packages rather than baking in workflow-specific features like subagents or plan mode.

That is accurate architecturally. The built-in shell is intentionally small at the agent level, but the surrounding customization surface is large.

## Core Primitives

### `AgentSession`

`AgentSession` is the central object. It is shared by interactive, print, and RPC modes and owns lifecycle, persistence, model/thinking state, compaction, bash execution, branching, extensions, and the active tool registry.

Key responsibilities:

| Primitive         | Role                                               |
| ----------------- | -------------------------------------------------- |
| `agent`           | Wrapped `@mariozechner/pi-agent-core` agent        |
| `sessionManager`  | JSONL persistence and tree navigation              |
| `settingsManager` | Runtime settings and mutable config                |
| `resourceLoader`  | Skills, prompts, themes, extensions, context files |
| `modelRegistry`   | Model lookup and auth resolution                   |
| tool registry     | Built-in, extension, and SDK tools                 |

The prompt pipeline is explicit: extension commands, input interception, skill/template expansion, queueing during streaming, model/auth validation, compaction precheck, `before_agent_start`, system-prompt override, then `agent.prompt()`.

### `AgentSessionRuntime`

`AgentSessionRuntime` owns the current `AgentSession` plus cwd-bound services. Its job is safe replacement: new session, resume, fork, import, and disposal.

The important pattern is the factory:

```ts
type CreateAgentSessionRuntimeFactory = (options: {
  cwd: string;
  agentDir: string;
  sessionManager: SessionManager;
  sessionStartEvent?: SessionStartEvent;
}) => Promise<CreateAgentSessionRuntimeResult>;
```

This lets Pi recreate services when the effective cwd changes. That is why `/resume`, `/fork`, `/new`, and import can replace the session without making the UI or RPC layers rebuild the world themselves.

### `AgentSessionServices`

This is the cwd-bound service bundle:

| Service           | Purpose                                      |
| ----------------- | -------------------------------------------- |
| `AuthStorage`     | Credentials                                  |
| `SettingsManager` | Global/project settings                      |
| `ModelRegistry`   | Built-in/custom models and auth              |
| `ResourceLoader`  | Extensions, skills, prompts, themes, context |
| diagnostics       | Non-fatal startup/runtime issues             |

### `SessionManager` And Session Tree

Sessions are JSONL files with entries linked by `id` and `parentId`, enabling in-place branching instead of copying whole histories.

Core entry types include:

| Entry                   | Meaning                             |
| ----------------------- | ----------------------------------- |
| `message`               | User, assistant, tool result, etc.  |
| `thinking_level_change` | Reasoning level changes             |
| `model_change`          | Model changes                       |
| `compaction`            | Summary plus first kept entry       |
| `branch_summary`        | Summary of abandoned branch         |
| `custom`                | Extension state, not in LLM context |
| `custom_message`        | Extension-provided LLM context      |
| `label`                 | Bookmarks/markers                   |
| `session_info`          | Display name                        |

The key algorithm is `buildSessionContext()`: it walks from selected leaf to root, applies model/thinking state, injects compaction summaries, keeps post-compaction messages, and includes branch/custom messages as appropriate.

### `ResourceLoader`

`ResourceLoader` is the customization aggregator. It exposes loaded extensions, skills, prompt templates, themes, context files, system prompt, appended prompt content, and dynamic resource extension.

`DefaultResourceLoader.reload()` resolves packages, CLI paths, auto-discovered resources, extension factories, skills, prompts, themes, `AGENTS.md`/`CLAUDE.md`, and system prompt sources.

Extensions can add resources dynamically through the `resources_discover` event; `AgentSession` then calls `resourceLoader.extendResources()` and rebuilds the system prompt.

### Extensions

Extensions are TypeScript modules loaded through `jiti`. They can subscribe to events, register tools, commands, shortcuts, flags, message renderers, and providers.

The API has two halves:

| Half            | Examples                                                                                                                   |
| --------------- | -------------------------------------------------------------------------------------------------------------------------- |
| Registration    | `on`, `registerTool`, `registerCommand`, `registerShortcut`, `registerFlag`, `registerMessageRenderer`, `registerProvider` |
| Runtime actions | `sendMessage`, `sendUserMessage`, `appendEntry`, `setSessionName`, `setActiveTools`, `setModel`, `setThinkingLevel`        |

The loader builds an `ExtensionAPI` where registration writes into an `Extension` object and runtime methods delegate through a shared runtime. `ExtensionRunner` binds that runtime to session operations, model registry operations, UI context, command context, and event emission.

### Tool Definitions

Pi uses definition-first tools. A `ToolDefinition` includes:

| Field                               | Purpose                          |
| ----------------------------------- | -------------------------------- |
| `name`, `label`, `description`      | LLM and UI identity              |
| `parameters`                        | TypeBox schema                   |
| `execute()`                         | Tool behavior                    |
| `renderCall`, `renderResult`        | TUI rendering                    |
| `promptSnippet`, `promptGuidelines` | System-prompt integration        |
| `executionMode`                     | Sequential or parallel execution |

Built-ins are `read`, `bash`, `edit`, `write`, `grep`, `find`, and `ls`, though the default active coding set is `read`, `bash`, `edit`, and `write`.

The tool layer is deliberately pluggable. Bash and edit use operation interfaces so execution and filesystem mutation can be delegated or swapped.

## Major Features

### 1. Multiple Run Modes

Pi has four main operation paths:

| Mode            | Purpose                                              |
| --------------- | ---------------------------------------------------- |
| Interactive TUI | Terminal UI with editor, footer, commands, renderers |
| Print text      | Single-shot prompt, final text output                |
| JSON mode       | Event stream for non-interactive consumers           |
| RPC mode        | JSONL command/event protocol over stdin/stdout       |
| SDK             | Direct programmatic embedding                        |

All of these route through `AgentSession` and `AgentSessionRuntime`, not separate implementations.

### 2. Session Branching And Compaction

Sessions are not linear logs. `/tree` navigates within the same file, `/fork` creates a new session file, and compaction stores a summary while retaining selected recent entries.

Manual compaction can be cancelled, replaced by an extension, or generated by Pi itself. Branch navigation can summarize abandoned branches.

### 3. Extension Event Surface

The extension lifecycle is broad. Events run from startup through `resources_discover`, input, provider request/response, tool call/result, compaction, tree navigation, model selection, and shutdown.

Practical capabilities include:

| Capability        | Mechanism                       |
| ----------------- | ------------------------------- |
| Permission gates  | `tool_call` can block           |
| Context injection | `before_agent_start`, `context` |
| Custom tools      | `registerTool()`                |
| Custom commands   | `registerCommand()`             |
| Custom providers  | `registerProvider()`            |
| Custom UI         | `ctx.ui` methods                |
| Session state     | `appendEntry()`                 |
| Custom compaction | `session_before_compact`        |
| Dynamic resources | `resources_discover`            |

### 4. Skills, Prompt Templates, Themes, Packages

Pi distinguishes several customization primitives:

| Primitive       | Shape                                | Role                                         |
| --------------- | ------------------------------------ | -------------------------------------------- |
| Skill           | Markdown `SKILL.md` with frontmatter | Command/resource available to model          |
| Prompt template | Markdown file with args              | Slash command expansion                      |
| Theme           | JSON/theme object                    | TUI styling                                  |
| Package         | npm/git/local bundle                 | Distributes extensions/skills/prompts/themes |

The package manager resolves resources across npm, git, local, user, project, temporary, and package origins.

### 5. Provider And Model System

`ModelRegistry` manages built-in and custom models, provider overrides, API key resolution, OAuth provider integration, and compatibility metadata.

The README lists broad provider support including Anthropic, OpenAI, Azure OpenAI, DeepSeek, Gemini, Vertex, Bedrock, Mistral, Groq, Cerebras, Cloudflare, xAI, OpenRouter, Vercel AI Gateway, ZAI, OpenCode, Hugging Face, Fireworks, Kimi, MiniMax, Xiaomi, and subscription providers.

### 6. Settings And Local Configuration

Settings merge global and project scopes, with nested objects merged recursively.

Important settings include model defaults, thinking level, transport, queue behavior, theme, compaction, branch summaries, retry behavior, shell path/prefix, packages, extensions, skills, prompts, themes, images, model cycling, tree filter mode, and session directory.

## High-Level Architecture

```text
CLI / SDK / RPC client
        |
        v
main.ts parses args and builds runtime factory
        |
        v
AgentSessionRuntime
        |
        v
cwd-bound AgentSessionServices
  - AuthStorage
  - SettingsManager
  - ModelRegistry
  - ResourceLoader
        |
        v
AgentSession
  - wraps @mariozechner/pi-agent-core Agent
  - owns tool registry
  - owns extension runner
  - owns session manager
  - builds system prompt
  - handles prompt queueing, compaction, retries
        |
        v
Provider stream via @mariozechner/pi-ai
        |
        v
AgentSession events
        |
        v
Interactive TUI / print JSON / RPC events / SDK subscribers
```

The key architectural separation is:

| Layer           | Owns                                                 |
| --------------- | ---------------------------------------------------- |
| CLI             | Argument parsing, startup shape, mode selection      |
| Runtime         | Session replacement and cwd-bound service lifecycle  |
| Services        | Auth, settings, model registry, resources            |
| Session         | Agent lifecycle, prompt pipeline, tools, persistence |
| Modes           | UI/protocol rendering and user interaction           |
| Extensions      | Cross-cutting behavior through events and APIs       |
| Package manager | Installed/discovered resource supply                 |

## What Matters Most

### The good architecture

Pi's best architectural move is making `AgentSession` mode-neutral. The TUI, print mode, RPC mode, and SDK all use the same session and runtime semantics. That keeps features like compaction, tree navigation, extension events, and tool activation consistent across interfaces.

### The distinctive primitive

The JSONL session tree is the standout primitive. A branchable session log with compaction and branch summaries is more powerful than a linear chat transcript and cheaper than copying sessions for every fork.

### The extension model is the product

Pi skips built-in plan mode and subagents. That only works because extensions are deep enough to intercept tools, mutate context, register providers, customize UI, and persist state.

### The risk

The extension surface is powerful but large. Extensions run with full system permissions, can mutate tools/system prompts/context, and can register providers. That is flexible, but trust and debugging become core product concerns.

## Transferable Concepts

| Concept                     | Why It Is Useful                                              |
| --------------------------- | ------------------------------------------------------------- |
| Mode-neutral session core   | Avoids duplicating behavior across TUI, CLI, RPC, SDK         |
| Branchable JSONL sessions   | Enables navigation, forking, summarization, and replay        |
| Resource loader abstraction | Makes skills/prompts/themes/extensions composable             |
| Definition-first tools      | Gives one source for schema, execution, prompt, and rendering |
| Extension lifecycle events  | Lets advanced workflows exist outside the core                |

## What Does Not Apply Everywhere

Subagent-free minimalism only works if extensions are first-class. If a project lacks this kind of event surface, "just use extensions" becomes hand-waving.

Runtime-reloadable TypeScript extensions are a tradeoff. They are great for local power users, less good for locked-down enterprise environments.

The TUI-specific render hooks are valuable for Pi, but they couple tool definitions to a terminal UI. That is acceptable for Pi because terminal experience is the product.

## Mapping Against go-agent

Pi validates `go-agent`'s current direction more than it suggests a pivot. The transferable lesson is not "build a coding agent shell"; it is "keep one mode-neutral runtime primitive and let every interface consume that same runtime." `go-agent` already has that shape through `Runner`, `RunRequest`, `Session`, `Event`, `Policy`, `Tool`, and provider-neutral `Model`.

The main caution: do not import Pi's extension/resource/package system into core. In `go-agent`, those belong as ordinary Go packages, examples, adapters, or a future optional CLI layer.

### Target Overview

Current `go-agent` reality:

| Area            | Current Shape                                                       |
| --------------- | ------------------------------------------------------------------- |
| Product center  | Embeddable Go runtime, not CLI/product shell                        |
| Core primitive  | `Runner` built from `Agent`                                         |
| Model boundary  | `Model.Turn(context.Context, TurnRequest)`                          |
| Tool boundary   | `Tool` plus `NewTool` / `NewToolWithSchema`                         |
| Session model   | Linear `Session` with pluggable `SessionStore`                      |
| Event model     | Structured `Event` stream plus `EventSink`                          |
| Policy model    | Host-owned `Policy` decisions for run start, tool call, tool result |
| Roadmap posture | CLI, MCP, sub-agent orchestration deferred/outside core             |

That matches `.agentera/vision.yaml`: library first, CLI second, platform never by default.

### Applicability Matrix

| Pi Concept                       |           Fit For `go-agent` | Recommendation                                                                                                                                                                           |
| -------------------------------- | ---------------------------: | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Mode-neutral runtime core        |                         High | Keep `Runner` as the single semantic core. CLI/service/worker/UI/RPC should consume `Run`/`Stream`, not fork runtime logic.                                                              |
| SDK-first exports                |                         High | `go-agent` already is the SDK. Prioritize ergonomic constructor/options next, because README examples still show a future `goagent.New` facade while implementation exposes `NewRunner`. |
| Event stream as integration seam |                         High | Lean harder into `Event` as the bridge for logs, UIs, replay, tests, CLI, and observability. This is the Go equivalent of Pi's shared session events.                                    |
| Definition-first tools           |                  Medium-High | Keep `Tool` small. Avoid Pi-style TUI render hooks in core. If needed, add optional metadata through separate interfaces/packages, not the root `Tool` contract.                         |
| Pluggable session persistence    |                         High | Current `SessionStore` is right. Pi's tree model is worth studying for future file-backed/replay stores, but not as a core requirement yet.                                              |
| Branchable JSONL sessions        |                       Medium | Useful for future local CLI/debug/replay package. Do not force branch trees into root `Session` until a concrete consumer needs forks/navigation.                                        |
| Compaction summaries             |                       Medium | Relevant when long-running sessions become real. Best modeled as host-owned session metadata or optional middleware/store behavior, not hidden core behavior.                            |
| Resource loader                  |                   Low-Medium | Do not copy Pi's resource loader into core. In Go, resource composition should be packages/config supplied by host apps. A future CLI may have its own loader.                           |
| Extension event bus              |                       Medium | `EventSink` and `Policy` cover the production-safe subset. Avoid a broad mutation-capable `ExtensionAPI` in core. Add narrower hooks only from real use cases.                           |
| Model registry                   | Low for core, Medium for CLI | Core should stay provider-interface based. A future optional CLI can have a registry; root package should not become a marketplace.                                                      |
| Package marketplace              |                          Low | Conflicts with ordinary Go packages over ecosystem ceremony. Use modules/adapters/examples instead.                                                                                      |
| Interactive TUI hooks            |                          Low | Explicit non-goal for core. A TUI can consume events later if needed.                                                                                                                    |

### Primitive Mapping

| Pi Primitive           | `go-agent` Equivalent                                                          | Gap                                                                                        |
| ---------------------- | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------ |
| `AgentSession`         | `Runner` + `Session` + `RunRequest`                                            | `go-agent` is intentionally less stateful; no session tree/compaction/runtime replacement. |
| `AgentSessionRuntime`  | Host application assembly around `Runner`                                      | No core runtime-replacement object needed unless a future CLI needs cwd/session switching. |
| `AgentSessionServices` | Host-owned dependencies: `Model`, `SessionStore`, `Policy`, `EventSink`, tools | Good fit. `go-agent` correctly makes these explicit constructor inputs.                    |
| `SessionManager`       | `SessionStore`                                                                 | Current interface is simpler. File-backed/tree-backed stores can be external packages.     |
| `ResourceLoader`       | Host app config/packages                                                       | Should stay outside root.                                                                  |
| `ExtensionAPI`         | `Policy`, `EventSink`, custom `Tool`, provider adapter packages                | Keep narrower and typed.                                                                   |
| `ToolDefinition`       | `Tool`, `ToolSpec`, `ToolSchema`                                               | Rendering/prompt snippets do not belong in root today.                                     |
| `ModelRegistry`        | Provider adapter packages implementing `Model`                                 | Registry only belongs in optional CLI/dev tooling.                                         |

### What To Borrow

| Pattern                                | Application In `go-agent`                                                                                                                |
| -------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| One runtime path for all consumers     | `examples/minimal`, `examples/service`, `examples/worker`, and `examples/cli` are the right proof: different host shapes, same `Runner`. |
| Events as the stable external protocol | A future CLI, debug UI, JSON mode, or RPC bridge should be a thin renderer over `Stream`, not a separate runtime.                        |
| Optional session-store packages        | A future `session/jsonl` or `session/file` package could explore Pi-like append-only replay without changing `SessionStore`.             |
| Strong host-owned policy               | `go-agent`'s `Policy` is more production-aligned for Go services than permission UI hooks.                                               |
| API promise alignment                  | The immediate gap is ergonomics: README uses `goagent.New(...)` and options while current implementation exposes `NewRunner(Agent)`.     |

### What Not To Borrow

| Pi Pattern                                      | Why Not                                                                                  |
| ----------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Runtime-loaded extension marketplace            | Too much ceremony and trust surface for a Go library core.                               |
| Prompt/theme/skill package manager              | Useful for a coding-agent product, not for an embeddable runtime.                        |
| TUI-specific tool rendering in tool definitions | Couples core tools to one interface. `go-agent` should let UIs render events externally. |
| Broad mutation-capable extension API            | Conflicts with inspectable, host-owned production control.                               |
| Built-in model registry                         | Provider adapters are enough for root; registry can be optional tooling.                 |

### Recommended Next Steps

1. Build the ergonomic constructor/options layer promised by the README: `New`, `WithModel`, `WithTool`, `WithPolicy`, `WithSessionStore`, `WithEventSink`.
2. Keep the optional developer CLI deferred, but define its rule now: it must be a consumer of `Runner.Stream`, not a second runtime.
3. Consider a file-backed `SessionStore` package later, possibly append-only JSONL, but do not add branch trees to root yet.
4. Expand event coverage only where production questions require it, such as tool duration, model latency, retry attempts, token usage, or provider metadata.
5. Treat Pi as validation for the architecture boundary: compact core, strong events, host-owned policy, and optional outer shells.
