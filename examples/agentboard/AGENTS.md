# Agentboard

- Build with `GOOS=wasip1 GOARCH=wasm go build -o agentboard.wasm ./app`.
- Run tests with `go test ./...`.
- Keep board authorization and workflow transitions in `app/domain.go`; both actions and the TUI must call that shared layer.
- Never persist raw SSH fingerprints in KV key paths or project membership metadata.
