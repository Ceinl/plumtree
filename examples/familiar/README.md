# familiar

**A summonable terminal spirit.** A GLM chat model living inside a Plumtree
leaf — the leaf form of ZCode, a coding agent. Connect over SSH and it
remembers you; ask from any script and it answers in one shot.

```
ssh -p 2222 <owner>/familiar@localhost          # interactive chat
ssh -p 2222 <owner>/familiar@localhost ask "why wasm?"   # one-shot, scriptable
```

## What it does

- **Interactive chat** — type, press enter, and the answer streams in
  character-by-character (enter skips the reveal). Every visitor gets their own
  conversation, keyed by SSH identity; anonymous visitors get an intentionally
  ephemeral one.
- **Durable memory** — `/remember likes tea` stores a note in the app's KV that
  is injected into the system prompt of every future question, across sessions.
  `/forget 2`, `/forget all`, `/memories` manage the list.
- **One-shot CLI** — `ask`, `history`, `remember`, `forget`, `memories`,
  `clear`, and `status` run over SSH exec with the same per-user state, so
  `… ask` works from shell scripts and other agents.
- **Presence** — when someone on the server asks the familiar a question, a
  one-line broadcast (`familiar/presence` pub/sub topic) appears in other open
  sessions: *mira: asked a question*. Prompt text is never broadcast.
- **Fail-closed setup hints** — every missing capability (no API key secret, no
  egress allowlist, no host commands) turns into an actionable notice, never a
  crash or a silent failure.

Slash commands in the TUI: `/help /new /name /remember /forget /memories
/retry /model /who /quit`.

## Secrets

| Secret               | Default                                      | Purpose                          |
|----------------------|----------------------------------------------|----------------------------------|
| `GLM_API_KEY`        | — (required)                                 | Bearer key sent upstream         |
| `GLM_MODEL`          | `glm-4.6`                                    | Model name                       |
| `GLM_BASE_URL`       | `https://api.z.ai/api/paas/v4/chat/completions` | Any OpenAI-compatible endpoint |
| `FAMILIAR_PERSONA`   | built-in ZCode persona                       | Full system-prompt override      |
| `FAMILIAR_TRANSPORT` | `curl`                                       | `fetch`, `curl`, or `anthropic` (see below)    |

## Transports (the honest part)

Plumtree's clean `fetch` capability sends **no request headers** by design
(`abi.FetchRequest` v1) — so no `Authorization` header can reach the API.
familiar therefore has three transports:

1. **`fetch`** — gated, default-deny egress; no extra server
   permissions. Use it with endpoints that need no key or take the key in the
   URL: put `{key}` anywhere in `GLM_BASE_URL` and the API key is substituted
   (query-escaped). A 401 over this transport explains the constraint.
2. **`curl` (default)** — runs `curl` through the host-command capability, which can send
   headers (`Authorization: Bearer …`). Set `FAMILIAR_TRANSPORT=curl` and ask
   the server operator to allowlist `curl` in `runtime.hostCommandAllowlist`.

3. **`anthropic`** — uses curl with the Anthropic messages format. Set
   `FAMILIAR_TRANSPORT=anthropic`, `GLM_BASE_URL` to the proxy base URL
   (the app appends `/v1/messages`), and `GLM_MODEL` to a supported model.
   This also requires the curl host-command allowlist.

Endpoints must use HTTPS. Numeric loopback HTTP addresses are allowed for
local development and tests. The default curl transport sends the API key in
an Authorization header; the server operator must allowlist curl.

Deployed as a paired owner (`pt deploy`), set the key:

```
pt secret set GLM_API_KEY=<key>
```

For `fetch`, also allow the endpoint host with `pt egress add <host>`.

## Developing

- `pt dev` — sandboxed WASM loop with KV + pub/sub only. Secrets, egress, and
  host commands are absent here **on purpose**; the leaf shows its fail-closed
  hints instead. Use it for UI work.
- `GLM_API_KEY=… FAMILIAR_TRANSPORT=curl go run ./app` — native run with real
  env secrets, real egress, and real host commands. Use it for end-to-end chat.
- `pt dev --headless --script '/,h,e,l,p,enter'` — deterministic scripted
  session, prints the frame.
- `go test ./app` — plumtest interaction tests (streaming reveal, slash
  commands, scrolling, persistence) plus wire tests of both transports against
  an in-process mock server.

## Why the ZCode desktop plan credential does not work

The free ZCode "Weekend Build" / start-plan grant lives in
`~/.zcode/v2/config.json` and authenticates against
`https://zcode.z.ai/api/v1/zcode-plan/…` — but that proxy is app-only by
design: requests must carry client-signing headers (timestamp, signature,
nonce, proof-of-work) and an Aliyun captcha verify token that only the desktop
app can produce. An external leaf gets `{"code":3007,"msg":"captcha verify
failed"}` regardless of headers. Building a bypass is out of scope; use a
regular API key instead (see Transports above) — any OpenAI-compatible
endpoint works via `curl` transport, e.g. a Z.ai trial key, bigmodel.cn's
free `glm-4.5-flash` tier, or an OpenRouter `:free` model:
