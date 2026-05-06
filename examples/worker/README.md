# Worker Example

Run a local background worker example:

```bash
go run ./examples/worker
```

The worker processes queued jobs with per-job deadlines, stable session IDs,
tool calls, and event logging through `New`. It uses a local model and a local
lookup tool so it runs without network access or API keys.
