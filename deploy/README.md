# Deploying Plumtree

Four services, built from this repo, wired by `docker-compose.yml`:

| Service | Image | Exposure |
|---|---|---|
| `control-plane` | `Dockerfile.control-plane` | `:8080` — dashboard/API; put a TLS reverse proxy in front |
| `ssh-gateway` | `Dockerfile.ssh-gateway` | `:2222` — public SSH; forward `:22` to it (or run it on 22) |
| `runner-broker` | `Dockerfile.runner-broker` | none — networkless; authenticated Unix socket from the gateway only |
| `build-worker` | `Dockerfile.build-worker` | none — internal network only |

## Quick start

```sh
cd deploy
cp .env.example .env      # fill in origin + four tokens (openssl rand -hex 32)
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

`PLUMTREE_ALLOW_HOST_COMMANDS=true` is available on the SSH gateway for trusted
self-hosted installations. It allows every claimed app to execute programs as
the gateway container user; unclaimed previews remain restricted. This bypasses
the normal WASM capability boundary by design. Leave it unset on multi-tenant
servers. In containers, only programs and files available inside the gateway
container are reachable unless the operator deliberately mounts more.

`PLUMTREE_AUTO_CLAIM_OWNER=HANDLE` is a separate trusted single-owner option on
the control plane. It claims each new deploy directly to that handle using the
shared deploy token and removes the Shoo browser step. Leave it empty for public
or multi-user servers.

- **build-worker has no internet egress.** It sits on an `internal: true`
  network with the control plane. Author builds resolve the unpublished
  SDK from module dirs baked into the image and their transitive deps from a
  baked-in `file://` module proxy — `GOPROXY` never touches the internet. The
  root filesystem is read-only; build sandboxes live on a size-capped tmpfs.
- **Session isolation.** The gateway sends each app session over an
  authenticated Unix socket to a separate, networkless runner container. That
  container has no gateway credential, host key, KV volume, or control-plane
  route; it uses a read-only root and bounded tmpfs scratch. A disposable
  `plumtree-runner-worker` process still wraps each wazero sandbox inside it.
- **Tokens.** Shared operator tokens are compared constant-time. Public `pt`
  binaries are generic and contain no deploy credential; authors configure a
  token at runtime with `pt configure --token`, which reads it without
  exposing it in shell history and stores it in a user-only config file. Keep
  the token narrowly scoped and rotate it if abused.
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

## Releases

Pull requests and pushes to `main` run `.github/workflows/ci.yml`, which checks
formatting, vets and tests every module declared by `go.work`, runs the race
detector, and cross-builds the public release contract.

Tagged pushes (`v*`) trigger `.github/workflows/release.yml`. Releases are
generic: server URLs and deploy credentials are runtime configuration and are
never embedded in public binaries. Each release publishes:

```text
pt-{linux,darwin}-{amd64,arm64}
pt-windows-amd64.exe
plumtree-server-{linux,darwin}-{amd64,arm64}
plumtree-server-windows-amd64.exe
checksums.txt
```

`plumtree-server` is the all-in-one control-plane binary used for local and
small self-hosted setups. It embeds the matching Plumtree SDK, TUI runtime, and
an offline module proxy, so in-process builds do not require a Plumtree source
checkout or network module resolution. A compatible Go toolchain must still be
available on `PATH` to compile deployed applications. The production topology
remains the separate containers described above. `checksums.txt` covers every
binary and is the machine-readable contract consumed by `ptinstall`.
