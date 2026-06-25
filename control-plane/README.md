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
- CI tokens.
- server-side secrets (metadata + values; values injected into claimed apps as
  `ctx.Env`, never returned by the API).
- per-app egress allowlist (injected into claimed apps as `ctx.Fetch`).
- quotas and authz, incl. `--max-deploys-per-hour` rate limiting and the
  per-app daily session cap.
- HTTP dashboard/API for owners to inspect their apps, plus claim-token
  `pt secret` / `pt egress` endpoints.
- Shoo-backed browser auth for the dashboard.
- local all-in-one SSH gateway for deployed apps during development, wiring the
  KV, pub/sub bus, Auth, Env, and Fetch capabilities per session.

Does not own:

- compiling untrusted source.
- production runner isolation (separate runner/gateway processes).

## Local dashboard

```bash
go run ./cmd/control-plane \
  -addr 127.0.0.1:18080 \
  -origin http://localhost:18080 \
  -dev-token local-dev \
  -ssh-addr 127.0.0.1:2222
```

The dashboard is served at `http://localhost:18080/dashboard`. Browser auth uses
Shoo (`https://shoo.dev`) and the API verifies every bearer `id_token`
server-side against Shoo's JWKS before returning owner/app data.

For local development, `-dev-token` enables `POST /api/dev/deploy`. Use the
same token with `pt deploy`; the CLI creates a short-lived anonymous deploy
claim, then `pt claim` opens the Shoo-authenticated browser claim page. Claim
links expire after 30 seconds. After a deploy is claimed, later `pt deploy`
updates use the saved claim token in `.plumtree/deploy.json`.

The same process also starts a local SSH gateway by default. After `pt deploy`,
connect to the deployed app without starting `pt dev --ssh`:

```bash
ssh <app>@plumtree.dev
```

Control-plane state is persisted by default in the OS config directory. On
macOS, that is:

```text
~/Library/Application Support/plumtree/control-plane-state.json
```

Override it with `-state-file PATH` or `PLUMTREE_STATE_FILE=PATH`. Pass
`-state-file ""` to keep state in memory only.
