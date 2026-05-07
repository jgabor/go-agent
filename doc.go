// Package goagent defines the public contract for embedding tool-using agents in Go programs.
//
// New is the low-ceremony facade for callers that already have resolved runtime
// dependencies. It applies runtime-owned defaults only: empty instructions, no
// tools, allow-all policy, no session persistence, no event sinks, retry
// disabled, and the runner's default per-run step limit. A streaming Model is
// required, so construction fails before any run starts when it is missing or
// invalid. Tests and local models that produce final responses can use
// ModelFromSimple to adapt SimpleModel into the canonical stream contract.
//
// Product assembly remains outside the core package. goagent does not load
// provider credentials, settings, prompts, resources, auth policy, UI state, CLI
// lifecycle, provider registries, MCP wiring, sub-agent orchestration, or workflow
// DSLs. Hosts assemble those concerns and pass ordinary Go dependencies in.
//
// Agent and NewRunner remain the explicit lower-level path. Calling New with
// options is equivalent to constructing the same Agent value and passing it to
// NewRunner.
//
// Retry is opt-in, bounded, policy-visible runtime behavior for model,
// runtime-owned, and retry-safe tool failures. DecisionRetry carries typed
// RetryContext, and EventRetry carries typed RetryEvent so retry attempts, skips,
// constraints, exhaustion, cancellation, and stop reasons are reconstructable
// through structured events. Tools must opt in through ToolSafety.Retryable
// before a failed call can be repeated, because repeating unsafe tools can
// duplicate side effects.
package goagent
