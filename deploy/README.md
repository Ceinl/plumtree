# Deploying Plumtree

Compose runs a combined control/gateway service and a networkless runner
service. The first service persists SQLite, KV, and the SSH host key. The
runner receives no database, KV, SSH, or network access. Only SSH is published.

## Quick start

```sh
cd deploy
cp .env.example .env
printf 'PLUMTREE_UID=%s\nPLUMTREE_GID=%s\n' "$(id -u)" "$(id -g)" >> .env
umask 077
mkdir -p data runner-socket runner-scratch
openssl rand 32 > database.key
openssl rand -hex 32 > runner.token
docker compose build
docker compose run --rm plumtree bootstrap \
  --config /etc/plumtree/config.json -handle alice -device laptop
docker compose up -d --build
```

The bootstrap command prints a one-use ID and secret. On the author device,
run `pt pair --bootstrap <id> --yes 127.0.0.1` and enter the secret when
prompted. The authority expires after ten minutes and is consumed atomically
with first-author registration. Routine container startup never prints a live
pairing secret.

The default endpoint is `0.0.0.0:2222`. Set
`PLUMTREE_SSH_PUBLISH_ADDR` to a dedicated application address when using port
22 so administrator SSH remains separate. `PLUMTREE_UID` and `PLUMTREE_GID`
must match the owner of the private secret and state files.

The service command is equivalent to:

```sh
plumtree serve --config /etc/plumtree/config.json \
  -storage-database-path /data/plumtree.db \
  -storage-kv-root /data/plumtree-data \
  -storage-ssh-identity /data/plumtree_host_key \
  -exposure-ssh-address :2222 \
  -runtime-runner-endpoint unix:///run/plumtree/runner.sock \
  -runtime-production true \
  -secrets-database-key-file /run/secrets/database-key \
  -secrets-gateway-token-file /run/secrets/runner-token \
  -product-version "$PLUMTREE_PRODUCT_VERSION"
```

Native and Compose startup use the same typed configuration loader. Every
persisted setting also has a one-run flag and environment form. For example,
`limits.maxSessions` maps to `-limits-max-sessions` and
`PLUMTREE_LIMITS_MAX_SESSIONS`.

## State and security

- The database, KV data, and host key are private volume data. Back up all
  three together with `plumtree state backup`.
- Production mode requires `secrets.databaseKeyFile`. Startup fails when the
  key is absent or the binary does not contain the qualified SQLCipher engine.
- The server persists a stable identity and rejects a changed host key for an
  existing database.
- Unknown device keys may use only the bounded pairing subsystem. Only active,
  enrolled device keys may use the control API subsystem.
- Compose drops all Linux capabilities, uses a read-only root filesystem, and
  bounds temporary storage and process resources.

## Qualification status

The native root server, clean API transport, local SDK builds, and Compose
configuration are covered by repository checks. A target-native SQLCipher
toolchain is required before producing release binaries for each OS/architecture.
The current qualification environment may not have those prefixes or cross
compilers; in that case the release gate must remain red.

Fresh native and Compose volumes use the same bootstrap, pairing, and control
journey. Deployed leaf serving remains a separate live-fixture release gate.
