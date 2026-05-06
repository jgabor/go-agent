# AGENTS.md

## Current Reality

- This repo is pre-implementation. The README quick-start/API examples are aspirational; the Features & Roadmap table is the current source of truth.
- There are no Go packages yet. Do not add placeholder runtime code just to make `go test ./...`, `go vet ./...`, `golangci-lint`, or `govulncheck` do work.
- Product direction lives in `.agentera/vision.yaml`: library-first Go agent runtime, CLI second, no hosted platform or workflow DSL by default.

## Commands

- Install tools: `go install github.com/magefile/mage@v1.17.2`; `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.1`; `go install golang.org/x/vuln/cmd/govulncheck@v1.3.0`.
- List targets: `mage -l`.
- Full local gate: `mage check`.
- Focused gates: `mage tidyCheck`, `mage test`, `mage vet`, `mage lint`, `mage vuln`.
- With no packages, package-dependent Mage targets intentionally print `skip <gate>: no Go packages yet` and succeed.
- Local hooks are optional: `lefthook install`. The configured hooks are Go-only and call Mage; they do not run Markdown/JSON formatters.

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
