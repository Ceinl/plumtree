# Plumtree — phased build plan

The product spec is in `PLATFORM_SPEC.md`; the subrepo map is in `REPOS.md`.
This file tracks *what is built* and *what is next*. Status as of 2026-06-25.

## Where it stands

The end-to-end author loop works against a local control plane:

```
pt new → pt dev → pt dev --ssh → pt deploy → pt claim → ssh <app>@plumtree.dev
```

A deployed app is built server-side from uploaded source, stored as a WASM
artifact, and streamed over SSH; every session runs in its own wazero sandbox
with no ambient authority. The first host capability — scoped **KV** — is wired
all the way through, so concurrent sessions of one app share persistent state.
`_devtest/chattest` is the proof: a shared chat room whose log lives in KV, so
every connected user sees the same conversation (refreshed by the host's
periodic repaint tick).

## Phases

### Phase 1 — WASM feasibility spike ✅
Confirmed the runtime + a real TUI loop compile and run under `wasip1` in
wazero, input/render bridged through the host renderer, with per-frame deadlines
and no raw-ANSI escape path. See `spike/` and the "Phase 1 findings" section of
`PLATFORM_SPEC.md`. Superseded by the production `runner`/`sdk`/`abi`; the spike
is kept only as the validating record.

### Phase 2 — SDK + runtime + author scaffold ✅
- `tui-runtime` (`github.com/Ceinl/plumtree/tui-runtime`) — extracted runtime: layout,
  components, screen/diff, keyboard, terminal.
- `sdk` (`github.com/Ceinl/plumtree/sdk`) — `RunTUI`/`CLI`, events, `Meta`, the versioned
  WASM ABI (`sdk/abi`), and native + `wasip1` build paths from one source.
- `pt new` / `pt dev` / `pt dev --headless` / `pt dev --ssh` — scaffold and run
  apps locally in wazero over a real PTY or SSH channel, with memory-page and
  per-frame deadline limits.

### Phase 3 — Deploy pipeline + control plane ✅
- `build-worker` (`github.com/Ceinl/plumtree/build-worker`) — sandboxed Go→WASM build with
  size/CPU/mem limits, isolated module cache, and checksum/module policy. Runs
  in-process inside the control plane or as a separate remote service
  (`-build-url`).
- `control-plane` (`github.com/Ceinl/plumtree/control-plane`) — owners/apps/deploys/artifacts/
  sessions persistence, Shoo browser auth, `pt deploy` via dev-token, a local
  all-in-one SSH gateway, session logs, kill switch, and concurrency caps.
- `pt deploy` / `pt claim` / `pt whoami` / `pt inspect` / `pt logs` — author-side
  deploy and inspection.

### Phase 4 — Host capabilities ✅
The capability surface beyond render/input, all sharing the same host-import +
ptr/len ABI pattern, native + `wasip1` SDK builds, and gateway wiring:

- **KV** (ABI v1) — `kv_get`/`set`/`delete`, `sdk.KV*`, `runner.Store` (mem +
  file, quotas), per-app shared store cached by app ID.
- **Pub/sub** (ABI v2) — `bus_sub`/`bus_pub` + `KindMessage`, `sdk.Subscribe`/
  `Publish` + `MessageMsg`, `runner.Bus`/`MemBus`/`Subscription` with the Source
  selecting on input + bus (live redraw, no polling).
- **Auth** — `auth_whoami` + `abi.Identity`, `sdk.Whoami`, `runner.Auth`/
  `StaticAuth`; the gateway captures the SSH key fingerprint per session.
- **Env / secrets** (claimed-only) — `env_get`, `sdk.Env`, `runner.Env`; secret
  *values* stored server-side (separate from metadata, persisted), injected for
  owned apps; `pt secret set|list|rm`.
- **Fetch / gated egress** (claimed-only) — `fetch`, `sdk.Fetch`/`Get`,
  `runner.AllowlistFetcher` (default-deny, subdomain match); per-app allowlist in
  the store; `pt egress add|list|rm`.

Example apps `kvcounter`, `buschat`, `fetchcheck` exercise the capabilities;
each has e2e tests that build the real WASM guest.

**Author auth is the deploy claim token** (`pt claim` + Shoo browser auth), not a
separate `pt auth login`. Secrets and egress are authorized by that claim token,
so claiming an app is what unlocks higher-trust capabilities — matching
"progressive trust = capability."

### Phase 5 — Production hardening (in progress)
- **Durable artifact storage** ✅ — a pluggable `control.BlobStore`: the default
  in-memory store embeds artifacts in the JSON snapshot (all-in-one dev), while
  `--blob-dir` switches to a filesystem store that keeps compiled WASM on disk,
  out of the metadata state file.
- **Out-of-process build worker** ✅ (already) — `--build-url` runs the
  `build-worker` as a separate service; in-process is the dev default.
- **Deploy-rate abuse control** ✅ — `--max-deploys-per-hour` caps new deploy
  claims platform-wide in a rolling hour (deploy gated harder than run),
  alongside the existing per-app daily session cap, concurrency caps, kill
  switch, and per-IP API rate limiting.

## What is next

In rough priority order:

1. **Finish the process split.** Promote `ssh-gateway/` from skeleton to a
   standalone serving front end, and run the WASM runner as a separate worker
   process/container from the control plane (proxying the `ctx` capabilities
   over the worker boundary). This is the largest remaining isolation gap.
2. **Anonymous preview deploy.** With deploy rate limiting and durable storage in
   place, add per-author quotas, reputation/moderation hooks, and claim-on-first
   -use, then open quota-gated anonymous deploy.
3. **`ctx.DB`** — richer scoped storage beyond KV, if app demand warrants it.

## Spike follow-ups carried forward

Structured color storage in `screen.Cell` (drop the SGR-string round-trip),
a guest bump/arena allocator instead of the `map[ptr][]byte` pin, finer frame
granularity / guest-side diffing, a TinyGo memory-floor comparison, and
wide/combining-rune width handling. Detail in `PLATFORM_SPEC.md` Phase 1
findings.
