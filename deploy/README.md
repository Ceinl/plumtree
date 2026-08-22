# Deploying Plumtree

Compose runs a combined control/gateway service and a networkless runner
service. The first service persists SQLite, KV, and the SSH host key. The
runner receives no database, KV, SSH, or network access. Only SSH is published.

## Quick start

```sh
cd deploy
cp .env.example .env
umask 077
openssl rand 32 > database.key
printf '%s' "$(openssl rand -hex 32)" > runner.token
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
22 so administrator SSH remains separate.

The service command is equivalent to:

```sh
plumtree serve --config /etc/plumtree/config.json \
  -product-version "$PLUMTREE_PRODUCT_VERSION"
```

Native and Compose startup use the same typed configuration loader. Every
persisted setting also has a one-run flag and environment form. For example,
`limits.maxSessions` maps to `-limits-max-sessions` and
`PLUMTREE_LIMITS_MAX_SESSIONS`.

## State and security

- The database, KV data, and host key are private volume data. Use the offline
  `plumtree state backup` and `plumtree state restore` commands on that volume.
- Production mode requires `secrets.databaseKeyFile`. Startup fails when the
  key is absent or the binary does not contain the qualified SQLCipher engine.
- The server persists a stable identity and rejects a changed host key for an
  existing database.
- Unknown keys cannot use the private control subsystem. They can use bounded
  pairing and the public or app-key leaf paths allowed by the selected app.
- Public and restricted leaf sessions share the SSH listener. The SQLite
  access policy is checked before artifact bytes enter a session.
- The gateway forwards hosted execution through an authenticated Unix socket.
  The runner is networkless, starts one disposable worker per session, drops
  all Linux capabilities, and has no control service credentials.

## Qualification status

The native root server, clean API transport, local SDK builds, and Compose
configuration are covered by repository checks. A target-native SQLCipher
toolchain is required before producing release binaries for each OS/architecture.
The current qualification environment may not have those prefixes or cross
compilers; in that case the release gate must remain red.

Fresh native and Compose volumes use the same bootstrap, pairing, deploy, and
direct SSH journey. Compose runs each hosted leaf through the isolated runner.
