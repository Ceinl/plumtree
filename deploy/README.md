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
# Create once locally, or have your secret manager write this path instead.
openssl rand -base64 32 > ./control-plane-state.kek
chmod 600 ./control-plane-state.kek
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
- **State encryption.** The control-plane snapshot (including app secret
  values) uses AES-256-GCM envelope encryption. Each write has a new data key;
  its wrapping key (KEK) comes from the `control_plane_state_kek` Docker secret
  mounted at `/run/secrets/`, outside `control-plane-data`. In production,
  startup refuses persistent state without this key. Per-app KV and the SSH host
  key currently live in `ssh-gateway-data`; protect that volume with encrypted
  storage and restrict its backup access.
- **Capacity limits.** Compose applies CPU, memory, and PID limits to every
  service. Builds have separate execution and waiting-queue caps. The control
  plane, gateway, and worker run with production safety checks and refuse to
  start if an owned critical limit is unlimited. An operator who intentionally
  accepts that risk must explicitly set
  `PLUMTREE_ACKNOWLEDGE_UNLIMITED_LIMITS=true` on the affected service.

## Key rotation and backups

Treat `control-plane-state.kek` as a key-encryption key, not as a backup
artifact. Store the source value in your managed secret store (for example,
cloud KMS/Secrets Manager or Vault) and have an agent or the orchestration
platform materialize the mounted file. The snapshot does not contain this key.

To rotate it, schedule a brief control-plane restart. Create a new 32-byte key
in the secret store, preserve the old version, and set the new material as
`PLUMTREE_STATE_ENCRYPTION_KEY_FILE` and the old material as
`PLUMTREE_PREVIOUS_STATE_ENCRYPTION_KEY_FILE`. Restart once with the rotation
overlay; it decrypts using either key and atomically re-encrypts the snapshot
with the new key:

```sh
docker compose -f docker-compose.yml -f docker-compose.rotate.yml up -d control-plane
```

Check the service logs, remove `PLUMTREE_PREVIOUS_STATE_ENCRYPTION_KEY_FILE`,
and restart with normal `docker-compose.yml`. Keep the old key in the secret
store, access-restricted, until every backup made under it has expired or been
re-encrypted. Do not delete or overwrite an old key just because the running
service has been rotated.

Back up `control-plane-data` and `ssh-gateway-data` together, using encrypted
backup storage with access limited to restore operators. Record the KEK version
that can decrypt each backup, but never include the KEK in the backup. Test a
restore at least quarterly in an isolated environment: restore both volumes,
mount the corresponding historical key from the secret store, and verify the
control plane starts and can serve a known app. A copied data volume without its
externally held KEK must be treated as unrecoverable by design.

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
