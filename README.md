# go-agent

go-agent gives any Go application a production agent runtime through one import.

It is an embeddable harness for tool-using agents that live inside your binary,
use your existing Go code, and report every turn, tool call, retry, and stop
reason through inspectable events. Add it to an API, worker, CLI, internal
platform, or domain library without adopting a hosted control plane or workflow
DSL.

go-agent ships the primitives. Your application owns the policy.

> Project status: this README describes the intended product shape. The [Features & Roadmap](#features--roadmap) table is
> the source of truth for what exists in this repository today.

## Table of Contents

- [Quick Start](#quick-start)
- [Why go-agent?](#why-go-agent)
- [Runtime Model](#runtime-model)
- [Providers & Models](#providers--models)
- [Tools](#tools)
- [Sessions](#sessions)
- [Streaming Events](#streaming-events)
- [Observability](#observability)
- [Policy Hooks](#policy-hooks)
- [Extensions](#extensions)
- [Development](#development)
- [Features & Roadmap](#features--roadmap)
- [Philosophy](#philosophy)
- [Contributing](#contributing)
- [License](#license)

---

## Quick Start

```bash
go get github.com/jgabor/go-agent
```

Create a runner, give it a model and tools, then call it from normal Go code:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	goagent "github.com/jgabor/go-agent"
)

func main() {
	ctx := context.Background()

	runner := goagent.New(
		goagent.WithModel(goagent.OpenAI{
			Model:  "gpt-4.1",
			APIKey: os.Getenv("OPENAI_API_KEY"),
		}),
		goagent.WithTool("weather", "Get the weather for a city", func(ctx context.Context, city string) (string, error) {
			return "72F and clear in " + city, nil
		}),
	)

	result, err := runner.Run(ctx, "Should I bring a jacket to Austin tonight?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Text)
}
```

That is the core promise: one import, one runner, normal Go functions as tools,
and a runtime that does not ask your application to move into somebody else's
platform.

## Why go-agent?

Most agent frameworks become one of three things:

- A hosted platform that wants to own deployment, state, evaluation, and UI.
- An orchestration framework that makes you learn a graph or workflow model.
- A provider abstraction layer that stops before the hard runtime problems.

go-agent is the smaller fourth option: an agentic runtime embedded directly in
your Go program. It manages the loop, tool dispatch, structured events,
sessions, cancellation, retries, and stop conditions while leaving product
policy, persistence, sandboxing, permissions, and orchestration shape to the
host application.

Use go-agent when:

- You already have a Go system and want agents inside it.
- Your tools are existing Go functions, services, repositories, or commands.
- You need observable production behavior, not a demo-only loop.
- You want extensibility through interfaces instead of a marketplace.
- You want to say no to platform defaults without rebuilding the agent loop.

## Runtime Model

go-agent centers on a few Go-native primitives:

| Primitive | Purpose                                                                 |
| --------- | ----------------------------------------------------------------------- |
| `Agent`   | Instructions, model settings, tools, and runtime policy.                |
| `Runner`  | Executes the agent loop with context cancellation and event emission.   |
| `Tool`    | A typed Go capability the model can request during a run.               |
| `Session` | Conversation state and resumable runtime data.                          |
| `Event`   | Streaming record of model output, tool calls, errors, and stop reasons. |
| `Policy`  | Host-owned approval, limits, validation, and safety decisions.          |

The runtime handles the repetitive machinery:

- Send input and session state to the model.
- Decode tool calls.
- Validate tool inputs.
- Execute tools with `context.Context`.
- Feed results back to the model.
- Stream events while the run proceeds.
- Stop on completion, error, policy decision, limit, or cancellation.

Your application keeps control of what matters:

- Which model providers are allowed.
- Which tools exist.
- Where sessions are stored.
- What requires approval.
- How execution is sandboxed.
- What observability backend receives events.
- How agent output becomes product behavior.

## Providers & Models

Providers are adapters, not the product. The runtime depends on a small model
interface and lets applications choose the provider package that matches their
environment.

```go
runner := goagent.New(
	goagent.WithModel(openai.Model("gpt-4.1")),
)
```

Provider adapters can support:

- OpenAI-compatible APIs
- Anthropic
- Google Gemini
- Azure OpenAI
- Amazon Bedrock
- Ollama
- Local or internal model gateways

Custom providers implement the same interface as built-in adapters. go-agent
does not need a model marketplace to call a model.

## Tools

Tools are ordinary Go capabilities wrapped with schema and metadata.

```go
type TicketLookup struct {
	ID string `json:"id" jsonschema:"description=Ticket ID"`
}

runner := goagent.New(
	goagent.WithTool("ticket.lookup", "Load an internal support ticket", func(ctx context.Context, input TicketLookup) (*Ticket, error) {
		return tickets.Get(ctx, input.ID)
	}),
)
```

Tool design follows Go conventions:

- `context.Context` controls cancellation and deadlines.
- Returned errors are real errors, not hidden transcript strings.
- Struct tags describe schemas where reflection is enough.
- Explicit tool definitions are available when reflection is not enough.
- Tool execution belongs to your process unless you choose otherwise.

## Sessions

Sessions carry working context across runs. The runtime treats persistence as an
interface because every production system already has opinions about storage.

```go
store := postgres.NewSessionStore(db)

runner := goagent.New(
	goagent.WithSessionStore(store),
)

result, err := runner.Run(ctx, "Summarize the last incident", goagent.WithSession("incident-42"))
```

Session stores can be in-memory for tests, file-backed for local tools, or
database-backed for services and workers.

## Streaming Events

Every run emits a stream of structured events.

```go
events, err := runner.Stream(ctx, "Investigate the failed deployment")
if err != nil {
	return err
}

for event := range events {
	switch event.Kind {
	case goagent.EventTextDelta:
		fmt.Print(event.Text)
	case goagent.EventToolCall:
		log.Printf("tool call: %s", event.ToolName)
	case goagent.EventStop:
		log.Printf("stopped: %s", event.StopReason)
	}
}
```

Events are meant for logs, traces, metrics, UIs, tests, and replay. If an agent
did something surprising, the host application should be able to reconstruct the
run without reading tea leaves from a final string.

## Observability

go-agent is observable by default. A production agent runtime should make these
questions cheap to answer:

- What did the model see?
- Which tool did it request?
- What input reached the tool?
- How long did the tool run?
- What error occurred?
- Why did the run stop?
- Which policy decision changed the path?

The runtime emits structured data that can be attached to OpenTelemetry,
application logs, audit trails, local debug UIs, or test assertions.

## Policy Hooks

go-agent does not ship permission-popup theater. Safety and approval flows must
match the host environment.

```go
runner := goagent.New(
	goagent.WithPolicy(goagent.PolicyFunc(func(ctx context.Context, decision goagent.Decision) error {
		if decision.ToolName == "deploy.production" {
			return requireChangeWindow(ctx, decision)
		}
		return nil
	})),
)
```

Policies can enforce:

- Tool allowlists and denylists
- Cost and token budgets
- Per-run step limits
- Human approval gates
- Tenant or user authorization
- Output validation
- Environment-specific restrictions

## Extensions

Extensions are Go code. Use interfaces, packages, and configuration the same way
you would for any production library.

Examples of extension patterns:

- Provider adapters
- Session stores
- Tool registries
- Event sinks
- Policy implementations
- Test harnesses
- CLI integrations
- MCP adapters outside the core
- Sub-agent orchestration outside the core

If a feature can be expressed as a normal Go package, it does not belong in the
core runtime by default.

## Development

The repository uses a small Go-first DX baseline:

- Go module: `github.com/jgabor/go-agent`
- Go version: `1.26.0` from `go.mod`
- Build automation: `mage`
- Linting: `golangci-lint` v2 with `goimports`, `gofumpt`, `errcheck`, `govet`, `ineffassign`, `staticcheck`, and `unused`
- Vulnerability scanning: `govulncheck`
- Local hooks: `lefthook`

Contributor and agent workflow details live in `AGENTS.md`. CI runs `mage check`
on pushes and pull requests targeting `main`.

## Features & Roadmap

This table reflects the repository today.

| Area                    | Intended capability                                     | Current status | Evidence                                   |
| ----------------------- | ------------------------------------------------------- | -------------- | ------------------------------------------ |
| Public API              | `Agent`, `Runner`, `Tool`, `Session`, `Event`, `Policy` | Started        | Root package contract exists               |
| Agent loop              | Model turn loop with tool dispatch and stop reasons     | Started        | `NewRunner` with tool dispatch loop        |
| Retries                 | Runtime retry policy and retry events                   | Deferred       | Retry semantics deferred by behavior tests |
| Tool schemas            | Go function and struct schema support                   | Started        | Struct inputs and explicit schemas exist   |
| Streaming               | Structured event stream for runs                        | Started        | Runner Stream with event correlation tests |
| Sessions                | Pluggable session storage                               | Started        | SessionStore and memory store exist        |
| Providers               | OpenAI-compatible provider adapter                      | Started        | `providers/openai` chat adapter exists     |
| Observability           | Event sink and OpenTelemetry integration                | Started        | EventSink hooks observe runtime events     |
| Policy hooks            | Approval, limits, validation, and authorization hooks   | Started        | Run/tool/result decisions in events        |
| Tests                   | Unit and integration coverage for runtime behavior      | Started        | API and behavior contract tests exist      |
| Examples                | Minimal app, service, worker, and CLI examples          | Not started    | No examples directory exists               |
| CLI                     | Optional developer CLI around the library               | Deferred       | Library-first direction                    |
| MCP adapter             | Optional adapter package outside the core               | Won't fix      | Deliberate non-goal for core               |
| Sub-agent orchestration | Optional coordination package outside the core          | Won't fix      | Deliberate non-goal for core               |

## Philosophy

go-agent is deliberately small so applications can be deliberately specific.

**No hosted control plane by default.** The runtime embeds into your deployment
model instead of replacing it.

**No built-in MCP requirement.** If MCP is useful, add an adapter package. The
core should not require it.

**No baked-in sub-agent hierarchy.** Coordination has many valid shapes. Build it
as an extension when the application needs it.

**No workflow DSL.** Go is already the orchestration language for Go systems.

**No model marketplace.** Providers are replaceable adapters. Your app chooses
what it can run.

**No permission-popup theater.** Policy must be real, contextual, and owned by
the host environment.

**No mandatory TUI or chat product.** go-agent is a runtime. UIs are consumers of
events, not the center of the architecture.

**No hidden shell.** Execution belongs to explicit tools with visible inputs,
outputs, and errors.

## Acknowledgements

Inspired by and modeled after [Pi](https://pi.dev).

## License

MIT. Jonathan Gabor ([@jgabor](https://github.com/jgabor)).
