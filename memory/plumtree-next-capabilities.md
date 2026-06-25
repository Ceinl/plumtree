---
name: plumtree-next-capabilities
description: Planned follow-ups after the KV capability in plumtree
metadata:
  type: project
---

After scoped KV ([[plumtree-platform]]), the natural follow-ups, in order:

1. ~~**Hosted KV wiring in control-plane.**~~ DONE (2026-06-25):
   `control-plane/internal/sshgateway/kv.go` `kvFor(appID)` caches a per-app
   `FileStore` in `Server.kvStores`, so concurrent deployed sessions share one
   instance.
2. ~~**Pub/sub shared rooms.**~~ DONE (2026-06-25): ABI v2 adds `KindMessage`
   (variable-length, inline topic+payload) and `bus_sub`/`bus_pub` host imports;
   `sdk.Subscribe`/`Publish` + `sdk.MessageMsg`; `runner.Bus`/`MemBus`/
   `Subscription` with `BusBinder` so `TTYSource` selects on input + bus and an
   idle session wakes on publish (no polling). Per-app `MemBus` cached by app ID
   in the gateway (`busFor`) and one per process in `pt dev`. `chattest` and the
   new `sdk/examples/buschat` use it; e2e tests build the real guest.
3. ~~**ctx.Auth.**~~ DONE (2026-06-25): `auth_whoami` host import + `abi.Identity`
   (flags+user encoding); `sdk.Whoami()`/`sdk.Identity`; `runner.Auth`/
   `StaticAuth`/`Identity` (per-session, not shared). Gateway captures the SSH
   key fingerprint via `PublicKeyCallback` (anonymous fallback id from session
   id) and threads `Identity` through handleSession→startSession. `buschat`
   shows the identity; e2e test asserts it.
4. ~~**ctx.Env secrets + pt secret.**~~ DONE (2026-06-25): `env_get` host import
   (`abi/env.go`); `sdk.Env` (wasip1 host / native reads os env); `runner.Env`/
   `MapEnv`. Control store keeps secret *values* separate from value-free
   `SecretMetadata` (`secretValues`, persisted as base64 in the snapshot);
   `UpsertSecret(Value)`, `DeleteSecret`, `ListSecrets`, `SecretsForApp`. HTTP
   `/api/dev/deploy/{id}/secrets` (claim-token auth, claimed-only, never returns
   values); gateway injects `Env` for owned apps only. `pt secret set|list|rm`.
   buschat shows a secret; e2e + httpapi + store tests.
5. ~~**ctx.Fetch gated egress.**~~ DONE (2026-06-25): `fetch` host import
   (`abi/fetch.go`, length-prefixed req/resp); `sdk.Fetch`/`Get`/`Response`
   (wasip1 gated / native real net); `runner.Fetcher`/`AllowlistFetcher`
   (default-deny, subdomain match, `ErrEgressDenied`). Control store per-app
   `egressAllow` (persisted) + `AddEgressHost`/`RemoveEgressHost`/
   `EgressAllowlist`; HTTP `/api/dev/deploy/{id}/egress` (claimed-only); gateway
   builds the fetcher for owned apps with a non-empty allowlist. `pt egress
   add|list|rm`; `sdk/examples/fetchcheck`; e2e + unit + httpapi tests.

Note: there is no separate `pt auth login` — author auth is the deploy **claim**
token (`pt claim` + Shoo browser auth). Secrets/egress are authorized by that
claim token. `ctx.DB` (richer scoped storage) remains as a future capability.
