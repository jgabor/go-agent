# Service Example

Run a small HTTP service embedding `go-agent`:

```bash
go run ./examples/service
```

Ask the service:

```bash
curl -sS localhost:8080/ask \
  -H 'Content-Type: application/json' \
  -d '{"input":"Should I bring a jacket in Berlin?","session_id":"demo"}'
```

The service uses `New`, `NewTool`, `SessionStore`, `Policy`, and `EventSink`
from the library. It keeps the model local so the example is
runnable without network access or API keys.
