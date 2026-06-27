# Plumtree Runner

Isolated service that runs deployed WASM apps per session.

Owns:

- wazero module instantiation.
- Plumtree host ABI imports.
- capability execution: `KV` (durable per-app state), `Bus`/`MemBus` (live
  pub/sub across sessions), `Auth` (per-session identity), `Env` (claimed-only
  secrets), and `Fetcher`/`AllowlistFetcher` (default-deny gated egress).
- session cancellation.
- memory, CPU, time, input, and output limits.
- structured frame rendering.
- CLI output filtering.
- **out-of-process isolation** — `ProcessRunner` + the `plumtree-runner-worker`
  binary (`cmd/plumtree-runner-worker`) run the wazero sandbox in a separate
  process, forwarding every host call to the parent over the lock-step
  `procproto`. The control plane enables it per session via `--runner-worker`.

Does not own:

- source builds.
- durable app ownership records.
- SSH protocol handling.
