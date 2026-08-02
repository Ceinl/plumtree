# Plumtree Runner Entry Points

The runner implementation now lives under `internal/runner` in the root
product module. This directory retains the existing broker and worker command
paths as temporary workspace entrypoints while the remaining product modules
move under the root module.

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
- **container isolation** — `plumtree-runner-broker` accepts authenticated Unix
  socket connections and spawns a disposable `plumtree-runner-worker` for each
  session. Production puts that broker in a networkless, read-only container
  with no gateway credentials or data volume. Every host call returns to the
  gateway over the lock-step `procproto`; local development can still use the
  weaker `--runner-worker` subprocess mode.

Does not own:

- source builds.
- durable app ownership records.
- SSH protocol handling.
