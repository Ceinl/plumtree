# Plumtree Subrepos

This workspace is split into local git repositories by product boundary.

| Path | Module | Purpose |
|------|--------|---------|
| `tui-runtime/` | `github.com/Ceinl/plumtree/tui-runtime` | Standalone TUI runtime extracted from `github.com/Ceinl/plums/internal/ui/tui`. |
| `sdk/` | `github.com/Ceinl/plumtree/sdk` | Author-facing Go SDK and WASM ABI wrapper. |
| `pt/` | `github.com/Ceinl/plumtree/pt` | Author CLI for scaffolding, local dev, auth, deploy, logs, and secrets. |
| `control-plane/` | `github.com/Ceinl/plumtree/control-plane` | Platform API, app/deploy metadata, auth, tokens, secrets, and quotas. |
| `build-worker/` | `github.com/Ceinl/plumtree/build-worker` | Sandboxed source-to-WASM build service. |
| `runner/` | `github.com/Ceinl/plumtree/runner` | Isolated WASM session runner and host capability implementation. |
| `ssh-gateway/` | `github.com/Ceinl/plumtree/ssh-gateway` | SSH server that maps connections to deployed app sessions. |
| `spike/` | `github.com/Ceinl/plumtree/spike` | Phase 1 WASM feasibility spike: guest counter (wasip1), wazero host runner, and ABI v0. Validates the load-bearing risk; not a shipped component. |

The architecture and product spec is in `PLATFORM_SPEC.md`; the phased build
plan is in `PLAN.md`.
