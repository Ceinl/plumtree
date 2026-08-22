# Deploying Plumtree

Compose runs the root-owned `plumtree` binary as one service. It persists the
SQLite database and SSH host key in one volume and publishes only the SSH
control transport; there is no public HTTP listener or shared bearer token.

## Quick start

```sh
cd deploy
cp .env.example .env
docker compose build
docker compose run --rm plumtree bootstrap \
  -database /data/plumtree.db -handle alice -device laptop
docker compose up -d --build
```

The bootstrap command prints a one-use ID and secret. On the author device,
run `pt pair --bootstrap <id> --yes 127.0.0.1` and enter the secret when
prompted. The authority expires after ten minutes and is consumed atomically
with first-author registration. Routine container startup never prints a live
pairing secret.

The default endpoint is `0.0.0.0:2222`. Set
`PLUMTREE_SSH_PUBLISH_ADDR` to a dedicated application address when using port
22 so administrator SSH remains separate.

The service command is equivalent to:

```sh
plumtree -database /data/plumtree.db \
  -host-key /data/plumtree_host_key \
  -ssh-addr :2222 \
  -product-version "$PLUMTREE_PRODUCT_VERSION"
```

## State and security

- The database and host key are private volume data. Back up both together and
  protect the backup with the operator's existing storage encryption.
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
