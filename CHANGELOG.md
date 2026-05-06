# Changelog

## [Unreleased]

### Added

- Added `NewRunner` implementing the core agent loop with model turns, tool dispatch, policy checks, event emission, session tracking, step limits, and cancellation.
- Added streaming behavior tests covering multi-turn tool dispatch, event ordering and correlation, error stops, cancellation, and Run/Stream parity.
- Added struct-input tool schema generation and `NewToolWithSchema` for explicit schema metadata.
- Added `SessionStore`, `RunRequest.SessionID`, and `NewMemorySessionStore` for resumable pluggable session storage.
- Added `NewTool` for adapting minimal `func(context.Context, string) (string, error)` tools with name and input validation.
- Added runtime behavior contract tests for completion, tool dispatch, tool errors, model errors, cancellation, policy denial, step limits, event ordering, and stop reasons.
- Added the root public API contract for agents, runners, models, tools, sessions, events, and policy decisions.

### Changed

- Deferred retry semantics explicitly in the roadmap until retry behavior can be specified without broad runtime scope.
- Updated the roadmap to reflect the started public API and contract test slices.
