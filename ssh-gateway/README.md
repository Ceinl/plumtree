# Plumtree SSH Gateway

SSH server that lets users run deployed apps with plain `ssh`. It runs either
embedded in the control plane (all-in-one) or as its own process/container
talking to a remote control plane.

Owns:

- Go `crypto/ssh` server.
- app handle parsing and PTY/session lifecycle.
- key input, resize, signal, and disconnect forwarding.
- the per-session WASM sandbox (in-process, or isolated via the runner worker).
- per-app KV stores (`--state-dir`) and the in-memory pub/sub bus.
- streaming host-rendered terminal output.

Does not own:

- app resolution, session accounting/quotas, or claimed capability config
  (secrets, egress) — those come from the control plane through `gateway.Backend`.
- deploy/build APIs.
- persistent control-plane state.

## Packages

- `gateway` — the server, decoupled from the control plane behind the
  `Backend` port. Embed it by setting `Server.Backend`.
- `gatewayapi` — the HTTP wire contract (DTOs, route prefix, auth header,
  error codes) shared by the control plane and the standalone client.
- `httpbackend` — a `gateway.Backend` that calls the control plane's
  operator-internal gateway API over HTTP.
- `cmd/ssh-gateway` — the standalone binary.

## Running standalone

The control plane must enable the gateway API (`--gateway-token <token>`), which
serves `/internal/gateway/*` guarded by that shared token. Then:

```sh
ssh-gateway \
  -control-url https://control.plumtree.dev \
  -gateway-token "$PLUMTREE_GATEWAY_TOKEN" \
  -ssh-addr 0.0.0.0:2222 \
  -state-dir /var/lib/plumtree-gateway \
  -runner-worker /usr/local/bin/plumtree-runner-worker
```

All flags also read from `PLUMTREE_*` environment variables (see `-h`).

In all-in-one mode the control plane embeds this `gateway` package directly via
an in-process `Backend` adapter, so there is no HTTP hop and no token needed.
