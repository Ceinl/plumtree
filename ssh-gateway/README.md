# Plumtree SSH Gateway

SSH server that lets users run deployed apps with plain `ssh`. It runs either
embedded in the control plane (all-in-one) or as its own process/container
talking to a remote control plane.

Owns:

- Go `crypto/ssh` server.
- app handle parsing and PTY/session lifecycle.
- key input, resize, signal, and disconnect forwarding.
- delegation of the per-session WASM sandbox to the production runner broker
  (or a local worker/in-process runtime for development only).
- per-app KV stores (`--state-dir`) and the in-memory pub/sub bus.
- streaming host-rendered terminal output.

Does not own:

- app resolution, session accounting/quotas, or claimed capability config
  (secrets, egress) — those come from the control plane through `gateway.Backend`.
- deploy/build APIs.

Apps that register SDK actions can be called non-interactively with
`ssh owner/app@host 'action <name> <json>'`. The gateway parses the fixed action
prefix and name, preserves the remainder as one bounded JSON argument, and
never invokes a shell. Ordinary CLI app arguments remain bounded,
whitespace-separated guest arguments.
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
  -runner-endpoint unix:///run/plumtree/runner.sock \
  -runner-token "$PLUMTREE_RUNNER_TOKEN"
```

All flags also read from `PLUMTREE_*` environment variables (see `-h`).
Production refuses to start without the remote broker boundary; the local
`-runner-worker` mode shares the gateway's OS authority and is for development.

In all-in-one mode the control plane embeds this `gateway` package directly via
an in-process `Backend` adapter, so there is no HTTP hop and no token needed.

## End-user authentication

SSH keys are optional. A client that proves possession of a public key gets a
stable fingerprint identity; the control plane marks it authenticated only when
that fingerprint is registered to an owner. An unregistered but proved key is
stable and unauthenticated. A client without a usable key takes the anonymous
fallback and receives an ephemeral `anonymous:<session-id>` identity.

The gateway deliberately does not accept SSH's `none` method: clients send
`none` before trying their keys, so accepting it would silently turn key-bearing
connections anonymous. Anonymous access instead uses a prompt-free
keyboard-interactive fallback after public-key authentication has been tried.
