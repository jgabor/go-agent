# AGENTS.md

## Current Reality

- This repo now has implemented Go runtime packages. README product sections remain partly aspirational; the Features & Roadmap table is the current source of truth for product/runtime capability status, not completed setup history.
- The runtime contract is streaming-primary: `Model.Stream(ctx, TurnRequest, emit func(Event)) error` is the provider seam, while `Run` results are assembled from canonical events; `ModelFromSimple` is the ergonomic adapter for tests and local final-response models.
- `providers/openai.ChatModel.Stream` is a direct OpenAI-compatible Chat Completions SSE adapter: it emits canonical text/tool-call/usage/finish/error events, emits success stops for completed assistant turns after usage, and leaves tool-call turns unstopped until Runner tool execution.
- Per-run controls live on `RunRequest` (`Instructions`, `ToolNames`, `Limits`) with distinct stop reasons for tool-call count, cumulative tool output, and wall-clock duration when configured.
- Event persistence and replay use `MarshalEvents`/`UnmarshalEvents` (v1 envelope); `RunRequest`/`Event` carry optional correlation (`RunID`, `ParentRunID`, `TaskID`, `Metadata`).
- Explicit policies see `EventPolicyPending` before `Decide`; denied tool calls can return a synthetic `PolicyDecision.ToolResult` instead of terminating the run.
- Optional `StreamingTool`/`EventToolProgress` emit bounded incremental tool output; rich `ToolResult` adds metadata, truncation or compression hints, `SourceRef`, and JSON-safe `Opaque` maps with `ValidateToolResult`.
- Hosts can call `ModelCapabilitiesOf` when a `Model` implements `ModelCapabilitiesProvider` (including `openai.ChatModel`); other models stay unchanged.
- Do not add placeholder runtime code just to make `go test ./...`, `go vet ./...`, `golangci-lint`, or `govulncheck` do work.
- Product direction lives in `.agentera/vision.yaml`: library-first Go agent runtime, CLI second, no hosted platform or workflow DSL by default.

## Commands

- Install tools: `go install github.com/magefile/mage@v1.17.2`; `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.1`; `go install golang.org/x/vuln/cmd/govulncheck@v1.3.0`.
- List targets: `mage -l`.
- Full local gate: `mage check`.
- Focused gates: `mage tidyCheck`, `mage test`, `mage vet`, `mage lint`, `mage vuln`.
- Package-dependent Mage targets run against the implemented Go packages; the empty-package skip behavior remains only as a fallback for future package removal.
- Local hooks are optional: `lefthook install`. The configured hooks call Mage for Go gates and Prettier for staged Markdown/JSON files.

## README And Roadmap Hygiene

- Keep detailed contributor, agent, tool-install, and verification instructions in `AGENTS.md`; keep `README.md` focused on product shape, public usage, and high-level contribution entry points.
- Keep README sections aspirational as if the product vision is fully realized. The Features & Roadmap table is the primary exception; elsewhere, make only surgical edits when realized implementation supersedes the aspirational shape.
- Do not use the README Features & Roadmap table to document every completed repository setup task. Excluded examples include README publication, `go.mod` initialization, DX baseline setup, and CI setup.
- Use the roadmap for user-visible or architecture-significant capabilities: public API, runtime loop, tools, streaming, sessions, providers, observability, policy hooks, tests, examples, and explicitly deferred extension surfaces.
- Keep roadmap statuses aligned with repository reality when product/runtime capabilities start or finish.

## Tooling Contracts

- Module path is `github.com/jgabor/go-agent`; Go version is `1.26.0` from `go.mod`.
- CI is `.github/workflows/ci.yml`; it runs `mage check` on pushes and PRs targeting `main`.
- `.golangci.yml` is golangci-lint v2 config with `goimports`, `gofumpt`, `errcheck`, `govet`, `ineffassign`, `staticcheck`, and `unused`.
- `.editorconfig` is part of the contract: tabs for Go; two spaces for Markdown, JSON, YAML, and shell files.
- `mage tidyCheck` runs `go mod tidy` and fails if `go.mod` or `go.sum` changed; review and stage the tidy result instead of bypassing it.

## Scope Guardrails

- Keep the core library-first: ordinary Go interfaces, `context.Context`, errors, tests, and modules before new abstractions.
- Do not add Wails, Bun, Playwright, AUR packaging, git-cliff release automation, schema generation hooks, MCP core requirements, baked-in sub-agent orchestration, or a workflow DSL unless a new plan explicitly calls for it.
- Public API names should stay plain Go vocabulary from the vision: `Agent`, `Runner`, `Tool`, `Session`, `Event`, `Policy`.

## Contribution Priorities

- Preserve the smallest public API that satisfies the README quick start.
- Build tests before broadening features.
- Keep examples honest and runnable.
- Resist adding platform features to the core.
