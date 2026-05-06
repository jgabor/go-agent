# CLI Consumer Example

Run a small command-line consumer of the library runtime:

```bash
go run ./examples/cli -input "Weather in Berlin?"
```

Stream structured events instead of final text:

```bash
go run ./examples/cli -input "Weather in Tokyo?" -stream
```

This is an example application, not a project CLI. It uses `New`, `NewTool`,
`SessionStore`, and `Stream` from the library while keeping the core product
library-first.
