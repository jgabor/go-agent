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
	"github.com/jgabor/go-agent/providers/openai"
)

func main() {
	ctx := context.Background()

	weather, err := goagent.NewTool("weather", "Get the weather for a city", func(ctx context.Context, city string) (string, error) {
		return "72F and clear in " + city, nil
	})
	if err != nil {
		log.Fatal(err)
	}

	runner, err := goagent.New(
		goagent.WithModel(openai.ChatModel{
			Model:  "gpt-4.1",
			APIKey: os.Getenv("OPENAI_API_KEY"),
		}),
		goagent.WithTools(weather),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := runner.Run(ctx, goagent.RunRequest{Input: "Should I bring a jacket to Austin tonight?"})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Text)
}
```

That is the core promise: one runner, normal Go functions as tools, and a
runtime that does not ask your application to move into somebody else's
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

Construction has two equivalent public paths. `New(options...)` is a facade for
already-resolved dependencies; explicit wiring through `Agent` and `NewRunner`
remains public and has the same runtime semantics.

The lower-level path is the same wiring without facade options:

```go
runner, err := goagent.NewRunner(goagent.Agent{
	Instructions: "Give practical weather advice.",
	Model:        model,
	Tools:        []goagent.Tool{weather},
})
```

Runtime-owned defaults applied by the facade:

- Empty instructions.
- No tools.
- Allow-all policy.
- No session persistence.
- No event sinks.
- Retry disabled unless `Retry` or `WithRetry` sets a bounded attempt budget.
- Runner default step limit when a run does not set one.

Outside core scope and always host-owned:

- Provider registry or credential discovery.
- Auth assembly and approval UI.
- Settings, prompt, skill, or resource loading.
- CLI, TUI, package lifecycle, or product shell behavior.
- MCP, sub-agent orchestration, or workflow DSL wiring.

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

Providers are adapters, not the product. The runtime depends on a small
streaming model interface and lets applications choose the provider package that
matches their environment.

```go
runner, err := goagent.New(
	goagent.WithModel(openai.ChatModel{
		Model:  "gpt-4.1",
		APIKey: os.Getenv("OPENAI_API_KEY"),
	}),
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

Custom providers implement the same stream-first interface as built-in adapters.
Tests, fakes, and local models that only have a final response can use
`ModelFromSimple` to convert that response into canonical events without writing
a provider-style streamer. go-agent does not need a model marketplace to call a
model.

## Tools

Tools are ordinary Go capabilities wrapped with schema and metadata.

```go
type TicketLookup struct {
	ID string `json:"id" jsonschema:"description=Ticket ID"`
}

lookup, err := goagent.NewTool("ticket_lookup", "Load an internal support ticket", func(ctx context.Context, input TicketLookup) (string, error) {
	ticket, err := tickets.Get(ctx, input.ID)
	if err != nil {
		return "", err
	}
	return ticket.Summary, nil
})

model := goagent.ModelFromSimple(goagent.SimpleModelFunc(func(ctx context.Context, req goagent.TurnRequest) (goagent.TurnResult, error) {
	return goagent.TurnResult{Message: goagent.Message{Content: "Loaded ticket."}}, nil
}))

runner, err := goagent.New(
	goagent.WithModel(model),
	goagent.WithTools(lookup),
)
```

Tool design follows Go conventions:

- `context.Context` controls cancellation and deadlines.
- Returned errors are real errors, not hidden transcript strings.
- Struct tags describe schemas where reflection is enough.
- `ToolDefinition` carries explicit schema, model-visible description, retry
  safety metadata, and execution constraints when reflection is not enough.
- Tool execution belongs to your process unless you choose otherwise.

Advanced definitions stay runtime-focused:

```go
deploy, err := goagent.NewToolFromDefinition(goagent.ToolDefinition{
	Name:        "deploy_status",
	Description: "Read deployment status for a service",
	Schema: goagent.ToolSchema{
		"type": "object",
		"properties": map[string]any{
			"service": map[string]any{"type": "string"},
		},
		"required": []string{"service"},
	},
	Function: func(ctx context.Context, service string) (string, error) {
		return deployments.Status(ctx, service)
	},
	Safety: goagent.ToolSafety{ReadOnly: true, Retryable: true},
	Constraints: goagent.ToolConstraints{
		Timeout:        2 * time.Second,
		MaxOutputBytes: 4096,
	},
})
```

The runtime exposes that metadata in model `ToolSpec` values, policy decisions,
and tool events. It does not accept UI renderers, product-shell settings,
marketplace lifecycle data, prompt/resource loading, MCP, sub-agent, or workflow
DSL concerns in the root tool surface.

## Sessions

Sessions carry working context across runs. The runtime treats persistence as an
interface because every production system already has opinions about storage.

```go
store := postgres.NewSessionStore(db)

runner, err := goagent.New(
	goagent.WithModel(model),
	goagent.WithSessionStore(store),
)

result, err := runner.Run(ctx, goagent.RunRequest{
	SessionID: "incident-42",
	Input:     "Summarize the last incident",
})
```

Session stores can be in-memory for tests, file-backed for local tools, or
database-backed for services and workers.

## Streaming Events

Every run emits a stream of structured events.

```go
events, err := runner.Stream(ctx, goagent.RunRequest{Input: "Investigate the failed deployment"})
if err != nil {
	return err
}

for event := range events {
	switch event.Kind {
	case goagent.EventResponseStart:
		log.Print("model response started")
	case goagent.EventContentBlockStart:
		log.Printf("content block: %s", event.BlockKind)
	case goagent.EventTextDelta:
		fmt.Print(event.Text)
	case goagent.EventMessageFinal:
		log.Printf("assistant message: %s", event.Message.Content)
	case goagent.EventToolCall:
		log.Printf("tool call: %s", event.ToolCall.Name)
	case goagent.EventRetry:
		log.Printf("retry %s attempt %d: %s", event.Retry.Target.Kind, event.Retry.Attempt, event.Retry.Outcome)
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
- Was retry considered, attempted, skipped, or exhausted?
- Why did the run stop?
- Which policy decision changed the path?

The runtime emits structured data that can be attached to OpenTelemetry,
application logs, audit trails, local debug UIs, or test assertions.

## Policy Hooks

go-agent does not ship permission-popup theater. Safety and approval flows must
match the host environment.

```go
runner, err := goagent.New(
	goagent.WithModel(model),
	goagent.WithPolicy(goagent.PolicyFunc(func(ctx context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionToolCall && decision.ToolCall.Name == "deploy.production" {
			return goagent.PolicyDecision{Allowed: false, Reason: "deploys require an approved change window"}, nil
		}
		return goagent.PolicyDecision{Allowed: true}, nil
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

Retry is also a policy-visible runtime decision. Hosts enable bounded model,
runtime-owned, and retry-safe tool retry with `Agent.Retry` or `WithRetry`;
`MaxAttempts` is the total attempt budget, including the first attempt. When
retry is considered, the runtime presents `DecisionRetry` with typed
`RetryContext`: target, reason, attempt count, retry budget, session/request
context, tool context when relevant, and the triggering error. Policy can allow,
deny, constrain `MaxAttempts`, or request a delay. Retry observations use
`EventRetry` with a typed `RetryEvent`, so attempts, skips, policy denials,
constraints, exhaustion, cancellation, and terminal stop reasons can be
reconstructed without a hidden lifecycle bus.

Tool retry is blocked unless `ToolSafety.Retryable` is set through
`ToolDefinition` or another advanced `Tool` implementation exposing
`ToolMetadata`. Unsafe tools therefore fail explicitly instead of being repeated
by the default allow-all policy.

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

| Area                    | Intended capability                                      | Current status | Evidence                                        |
| ----------------------- | -------------------------------------------------------- | -------------- | ----------------------------------------------- |
| Public API              | `Agent`, `Runner`, `Tool`, `Session`, `Event`, `Policy`  | Started        | Root package contract and `New` facade exist    |
| Agent loop              | Streaming model loop with tool dispatch and stop reasons | Started        | `NewRunner` consumes canonical model streams    |
| Retries                 | Runtime retry policy and retry events                    | Started        | Observable model/runtime/tool retry exists      |
| Tool schemas            | Go function and advanced tool metadata support           | Started        | Struct inputs and `ToolDefinition` exist        |
| Streaming               | Structured event stream for runs                         | Started        | Runner Stream and stream assembly tests         |
| Sessions                | Pluggable session storage                                | Started        | SessionStore and memory store exist             |
| Providers               | OpenAI-compatible provider adapter                       | Started        | `providers/openai` streams Chat Completions SSE |
| Observability           | Event sink and OpenTelemetry integration                 | Started        | EventSink hooks observe runtime events          |
| Policy hooks            | Approval, limits, validation, and authorization hooks    | Started        | Run/tool/result decisions in events             |
| Tests                   | Unit and integration coverage for runtime behavior       | Started        | API and behavior contract tests exist           |
| Examples                | Minimal app, service, worker, and CLI examples           | Started        | Examples use `New` with local models            |
| CLI                     | Optional developer CLI around the library                | Deferred       | Library-first direction                         |
| MCP adapter             | Optional adapter package outside the core                | Won't fix      | Deliberate non-goal for core                    |
| Sub-agent orchestration | Optional coordination package outside the core           | Won't fix      | Deliberate non-goal for core                    |

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
