# Minimal App Example

Run the smallest local app with no network calls or API keys:

```bash
go run ./examples/minimal
```

The example wires `NewRunner`, `NewTool`, a tiny local model, and an
`EventSink`. It mirrors the README shape honestly with the API that exists
today, while the aspirational `goagent.New` facade remains a future slice.
