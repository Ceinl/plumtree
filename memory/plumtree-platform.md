---
name: plumtree-platform
description: What the plumtree project is and where its build stands
metadata:
  type: project
---

`/Users/c/code/plumtree` is a multi-module Go workspace (go.work) for a platform
that hosts Go TUI/CLI apps compiled to WASM and streamed over SSH — "Lakebed for
the terminal." Modules: `sdk`, `pt` (author CLI), root `internal/runner`,
root `internal/control`, `internal/httpapi`, `internal/auth`,
`internal/gatewaybackend`, and `internal/gateway` (SSH front end and control
client), plus the temporary `control-plane` and `build-worker` modules.
Architecture, subrepo map, and status are all now
consolidated in the root `README.md` (the old `PLATFORM_SPEC.md`/`PLAN.md`/
`REPOS.md` were folded into it and deleted 2026-06-27). Now a single top-level git
repo (monorepo) at `/Users/c/code/plumtree`; `_devtest/` and `sdk/plums` are
gitignored/untracked local state. The Phase-1 `spike/` prototype was removed
2026-07-08 (validated + superseded; recoverable from git history).

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
Production hardening (Phase 5, done): out-of-process runner isolation
(`internal/runner.ProcessRunner` + `runner/cmd/plumtree-runner-worker`, forwards every host call
over the lock-step `procproto`; control-plane `--runner-worker`); durable
artifact storage (`control.BlobStore` + `--blob-dir`); out-of-process build
worker (`--build-url`); deploy-claim rate limiting (`--max-deploys-per-hour`);
anonymous preview run (`--anonymous-preview`, `ssh preview-<deployID>@host`,
ownerless tightest sandbox). Module paths were renamed to
`github.com/Ceinl/plumtree/<sub>` (was `plumtree.dev/*`). The SSH gateway now
runs either embedded in the control plane or through the standalone root-owned
gateway role. Remaining: moderation at scale, `ctx.DB`. See
[[plumtree-next-capabilities]].

Note: `sdk/plums` is an independent checkout (github.com/Ceinl/plums, the
original TUI source repository), with its own .git, untracked/gitignored in sdk
— not part of sdk's build. As of 2026-06-25 its AI-agent app (cmd/api/app/core/
debuglog/keyboard) was deleted; only the TUI component library under
`internal/ui` (~5.3k LOC) remains as a reusable TUI base.
