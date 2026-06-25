---
name: plumtree-platform
description: What the plumtree project is and where its build stands
metadata:
  type: project
---

`/Users/c/code/plumtree` is a multi-module Go workspace (go.work) for a platform
that hosts Go TUI/CLI apps compiled to WASM and streamed over SSH — "Lakebed for
the terminal." Subrepos: `tui-runtime`, `sdk`, `pt` (author CLI), `runner`
(wazero session runner + host ABI), `control-plane`, `build-worker`,
`ssh-gateway` (skeleton). Architecture in `PLATFORM_SPEC.md`, subrepo map in
`REPOS.md`. Each subrepo is its own git repo; the top-level dir, `spike/`, and
`_devtest/` are NOT under git.

Status (2026-06-25): Phase 1 spike validated; end-to-end dev+deploy loop works
(`pt new → dev → dev --ssh → deploy → claim → ssh <app>@plumtree.dev`). The full
host-capability surface is built, all on the same host-import + ptr/len ABI
(now abi.Version=2), with native + wasip1 SDK builds, gateway wiring, example
apps, and e2e tests that build the real WASM guest:
- **KV** (durable state), **pub/sub** (`bus_sub`/`bus_pub` + `KindMessage`,
  live cross-session redraw, no polling), **Auth** (`auth_whoami`, SSH-key
  fingerprint), **Env** (claimed-only secrets, `pt secret`), **Fetch**
  (claimed-only default-deny gated egress, `pt egress`).
Auth = the deploy **claim** token (`pt claim` + Shoo); NO `pt auth login`.
Production hardening started: durable artifact storage (`control.BlobStore` +
`--blob-dir`), out-of-process build worker (`--build-url`, pre-existing), and
deploy-claim rate limiting (`--max-deploys-per-hour`). Remaining: runner/gateway
as separate processes, anonymous preview deploy. See [[plumtree-next-capabilities]].

Note: `sdk/plums` is an independent checkout (github.com/Ceinl/plums, the repo
tui-runtime was extracted from), with its own .git, untracked/gitignored in sdk
— not part of sdk's build. As of 2026-06-25 its AI-agent app (cmd/api/app/core/
debuglog/keyboard) was deleted; only the TUI component library under
`internal/ui` (~5.3k LOC) remains as a reusable TUI base.
