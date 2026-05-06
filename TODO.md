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

## Now

- [ ] Implement minimal function tools for the README quick start.
  - Acceptance: ordinary Go functions can be registered as tools for the quick-start case.
  - Acceptance: tool execution receives `context.Context` and returns real Go errors.
  - Acceptance: requested tool names and inputs are validated before execution.
  - Acceptance: unsupported function shapes fail clearly rather than becoming placeholder schema support.

- [ ] Implement the core agent loop.
  - Acceptance: `Runner` sends user input and session state to the model interface.
  - Acceptance: requested tools are validated, executed with `context.Context`, and fed back to the model.
  - Acceptance: runs stop on completion, error, policy denial, step limit, or cancellation.
  - Acceptance: the loop is usable by `Run` and does not require a hosted platform, workflow DSL, or background shell.

- [ ] Implement minimal policy hooks in the core loop.
  - Acceptance: default policy allows safe execution without extra configuration.
  - Acceptance: host applications can deny tool execution before the tool runs.
  - Acceptance: policy denial produces a stop reason and structured event.
  - Acceptance: advanced allowlists, budgets, approvals, authorization, and validation can be added without replacing the core interface.

- [ ] Implement structured streaming events.
  - Acceptance: runs emit events for text deltas, tool calls, tool results, errors, policy decisions, and stops.
  - Acceptance: events include enough ordering and correlation data to pair tool calls with results and reconstruct a run.
  - Acceptance: stream consumers can reconstruct a run without parsing final text.
  - Acceptance: event structs are suitable for logs, traces, UIs, tests, and replay without exposing provider-specific internals.

## Next

- [ ] Expand tool schema support.
  - Acceptance: struct inputs can produce JSON-schema-like metadata from tags where reflection is enough.
  - Acceptance: explicit tool definitions are available when reflection is not enough.
  - Acceptance: schema metadata remains ordinary Go code and does not introduce a workflow DSL.

- [ ] Add pluggable session storage.
  - Acceptance: runtime depends on a session store interface, not a concrete database.
  - Acceptance: session state defines what model transcript, tool results, and runtime metadata are persisted.
  - Acceptance: in-memory session storage supports tests and examples.
  - Acceptance: callers can resume a run with a stable session identifier.

- [ ] Expand policy hooks.
  - Acceptance: host applications can allow, deny, or constrain tool execution.
  - Acceptance: policies can enforce allowlists, budgets, step limits, approvals, authorization, output validation, and environment restrictions.
  - Acceptance: policy decisions are visible through structured events.
  - Acceptance: policies remain host-owned and do not become permission-popup theater.

- [ ] Add an OpenAI-compatible provider adapter.
  - Acceptance: adapter packaging matches the API decision from the first slice.
  - Acceptance: API keys and model names are supplied by the host application.
  - Acceptance: tests avoid live network calls unless explicitly marked integration-only.
  - Acceptance: runtime core still depends on the model interface, not provider packages.

- [ ] Add observability integration points.
  - Acceptance: applications can attach event sinks without changing run behavior.
  - Acceptance: OpenTelemetry integration can record model turns, tool calls, latency, errors, policy decisions, retries, and stop reasons where supported.
  - Acceptance: observability remains optional and does not impose a hosted control plane.

## Examples

- [ ] Add a minimal app example.
  - Acceptance: example is runnable and mirrors the README quick start.

- [ ] Add a service example.
  - Acceptance: example shows embedding the runtime in a server-style Go application.

- [ ] Add a worker example.
  - Acceptance: example shows cancellation, deadlines, retries if implemented, or background processing.

- [ ] Add a CLI example.
  - Acceptance: example demonstrates a command-line consumer without turning the project into a CLI-first product.

## Deferred

- [ ] Consider an optional developer CLI after the library runtime is useful.
  - Trigger: core API, runtime loop, events, sessions, policy hooks, tests, and examples are in place.
  - Constraint: CLI must wrap the library; it must not become the product center.

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
