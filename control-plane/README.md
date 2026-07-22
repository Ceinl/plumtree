# Plumtree Control Plane

Platform API and persistent state for Plumtree.

Owns:

- owners.
- apps.
- deploys.
- artifacts metadata, and artifact bytes (in-memory in the state file by
  default, or on disk via `--blob-dir` for durable artifact storage).
- sessions metadata.
- SSH keys.
- server-side secrets (metadata + values; values injected into claimed apps as
  `ctx.Env`, never returned by the API).
- per-app egress allowlist (injected into claimed apps as `ctx.Fetch`).
- quotas and authz, incl. `--max-deploys-per-hour` rate limiting and the
  per-app daily session cap.
- optional remote WASM isolation for embedded SSH via `--runner-endpoint` and
  `--runner-token`; production embedded SSH requires it. Local development may
  use the weaker `--runner-worker <path>` subprocess fallback.
- optional anonymous preview run via `--anonymous-preview` (run any deploy
  unclaimed at `ssh preview-<deployID>@host` in the tightest sandbox).
- HTTP dashboard/API for owners to inspect their apps, plus claim-token
  `pt secret` / `pt egress` endpoints.
- Shoo-backed browser auth for the dashboard.
- local all-in-one SSH gateway for deployed apps during development, embedding
  the `ssh-gateway` package via an in-process backend and wiring the KV, pub/sub
  bus, Auth, Env, and Fetch capabilities per session.
- the operator-internal gateway API (`/internal/gateway/*`, enabled with
  `--gateway-token`) that lets a standalone `ssh-gateway` use the control plane
  as its backend.

Does not own:

- compiling untrusted source.
- the SSH front end itself in production — that is the separate `ssh-gateway`
  deployable, talking to this control plane over the gateway API.

## Local dashboard

```bash
go run ./cmd/control-plane
```

The dashboard is served at `http://localhost:8080/dashboard`, and SSH listens
on `127.0.0.1:2222`. On the first non-production start, the server generates a
private deploy token under the OS user config directory. A local `pt` process
automatically reads that token, so `pt deploy` needs no connection setup when
the server and CLI run as the same user on the same machine.

To serve over Tailscale instead, use:

```bash
go run ./cmd/control-plane --tailscale
```

The server detects its Tailscale IPv4 address, binds HTTP and SSH to that
address, and prints the `pt configure` command and token needed by another
machine. Explicit `-addr`, `-origin`, and `-ssh-addr` values still take
precedence. Plain startup remains loopback-only; `--tailscale` is the explicit
opt-in to network access.

Browser auth uses
Shoo (`https://shoo.dev`) and the API verifies every bearer `id_token`
server-side against Shoo's JWKS before returning owner/app data.

For local development, the generated token enables `POST /api/dev/deploy`.
Pass `-dev-token TOKEN` or set `PLUMTREE_DEV_TOKEN` to replace it; explicitly
setting either to an empty value disables deploys. The CLI creates a short-lived anonymous deploy
claim, then `pt claim` opens the Shoo-authenticated browser claim page. Claim
links expire after 5 minutes. After a deploy is claimed, later `pt deploy`
updates use the saved claim token in `.plumtree/deploy.json`.

The same process also starts a local SSH gateway by default. After `pt deploy`,
connect to the deployed app without starting `pt dev --ssh`:

```bash
ssh -p 2222 -o HostKeyAlias=plumtree-dev -o StrictHostKeyChecking=accept-new <app>@127.0.0.1
```

The server does not modify `~/.ssh/config` by default. Pass
`-ssh-host plumtree-local` (or set `PLUMTREE_SSH_HOST`) to opt into a managed
alias and connect with `ssh <app>@plumtree-local`.

Control-plane state is persisted by default in the OS config directory. On
macOS, that is:

```text
~/Library/Application Support/plumtree/control-plane-state.json
```

Override it with `-state-file PATH` or `PLUMTREE_STATE_FILE=PATH`. Pass
`-state-file ""` to keep state in memory only.

The generated deploy token is stored beside the state under
`plumtree/dev-token` with mode `0600`. `PLUMTREE_DEV_TOKEN_FILE` selects another
path. Production mode never generates a token. Set `PLUMTREE_TAILSCALE=true` as
the environment equivalent of `--tailscale`.
