# Deploying Plumtree

Three services, built from this repo, wired by `docker-compose.yml`:

| Service | Image | Exposure |
|---|---|---|
| `control-plane` | `Dockerfile.control-plane` | `:8080` — dashboard/API; put a TLS reverse proxy in front |
| `ssh-gateway` | `Dockerfile.ssh-gateway` | `:2222` — public SSH; forward `:22` to it (or run it on 22) |
| `build-worker` | `Dockerfile.build-worker` | none — internal network only |

## Quick start

```sh
cd deploy
cp .env.example .env      # fill in origin + three tokens (openssl rand -hex 32)
docker compose up -d --build
```

Smoke test:

```sh
curl -s http://localhost:8080/          # dashboard
ssh -p 2222 <owner>/<app>@<host>        # once something is deployed
```

## Security posture

- **build-worker has no network.** It sits on an `internal: true` network,
  reachable only from the control plane. Author builds resolve the unpublished
  SDK from module dirs baked into the image and their transitive deps from a
  baked-in `file://` module proxy — `GOPROXY` never touches the internet. The
  root filesystem is read-only; build sandboxes live on a size-capped tmpfs.
- **Session isolation.** The gateway runs every app session in a separate
  `plumtree-runner-worker` process (baked into its image) on top of the wazero
  sandbox.
- **Tokens.** All three shared tokens are compared constant-time. The
  `PLUMTREE_DEV_TOKEN` ends up inside published `pt` binaries — treat it as
  build config, not a secret, and rotate it if abused (see
  `.github/workflows/release.yml`).
- **State.** Control-plane state is a JSON snapshot plus a blob dir in the
  `control-plane-data` volume; per-app KV and the SSH host key live in
  `ssh-gateway-data`. Back both volumes up.

## Updating the SDK

The build-worker image bakes `sdk/` and `tui-runtime/` at build time — rebuild
it (`docker compose build build-worker`) whenever they change, or deployed
authors compile against a stale SDK.

## Releasing pt

Tagged pushes (`v*`) trigger `.github/workflows/release.yml`, which bakes the
server URL and deploy token into the published binaries. Set the repo secrets
once:

```sh
gh secret set PLUMTREE_SERVER_URL --body "https://plumtree.example.com"
gh secret set PLUMTREE_DEV_TOKEN  --body "<same value as .env>"
```
