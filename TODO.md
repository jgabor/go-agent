# TODO

Source of truth for product/runtime capability status: `README.md` Features &
Roadmap table. This backlog breaks that roadmap into executable slices and may
also track repository bookkeeping that should not appear in the README roadmap.

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
- [x] Complete Aila Runtime Gap Features plan (tasks 1–6): run overrides and limits, JSON replay and correlation, policy pending and recoverable denials, rich tool results, streaming tool progress, optional model capability hints.

## Now

## Next

## Examples

## Deferred

- [ ] Consider an MCP adapter package outside the core.
  - Trigger: a concrete integration need exists.
  - Constraint: core runtime must not require MCP.

- [ ] Consider a sub-agent coordination package outside the core.
  - Trigger: a concrete application needs reusable coordination primitives.
  - Constraint: core runtime must not bake in a sub-agent hierarchy or workflow DSL.

## Maintenance

- [ ] Keep `README.md` roadmap status aligned with repository reality after each completed item.
- [ ] Run `mage check` before proposing changes.
- [ ] Avoid placeholder runtime code whose only purpose is making package-dependent gates do work.
