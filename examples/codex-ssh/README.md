# Codex over SSH

A narrow SSH-to-Codex bridge for a trusted, self-hosted Plumtree server. The
app accepts a prompt as its SSH exec command and invokes `codex exec` on the
server. It is deliberately restricted to the claimed app owner or explicitly
allowlisted SSH fingerprints; exposing an AI agent and its credentials as an
anonymous public endpoint would be unsafe.

## Server setup

1. Install and authenticate the Codex CLI as the OS user running Plumtree.
2. Enable `allowHostCommands` in the control-plane server configuration (or
   start it with `-allow-host-commands`).
3. Deploy and claim this app. Host commands never run for unclaimed previews.
4. For an auto-claimed deployment, allow your proved SSH-key fingerprint with
   `pt secret set CODEX_ALLOWED_SSH_KEYS`. Find it with
   `ssh-keygen -lf ~/.ssh/id_ed25519.pub -E sha256`; multiple fingerprints may
   be separated by spaces or commas. Auto-claim's synthetic owner can never
   satisfy the normal owner check, so this step is required in that mode.
5. Optionally set `CODEX_WORKDIR` and `CODEX_SANDBOX` (`read-only` or
   `workspace-write`) with `pt secret set`.

The default sandbox is `read-only`, and every run is ephemeral.

```bash
ssh <owner>/codex-ssh@plumtree.app 'summarize this repository'
ssh <owner>/codex-ssh@plumtree.app 'find the cause of the failing tests'
```

For the local auto-claim setup shown by `pt deploy`, the equivalent command is:

```bash
ssh -p 2222 autoclaim/codex-ssh@<host> 'summarize this repository'
```

This is command rerouting, not a fully interactive terminal proxy: Plumtree
starts one non-interactive `codex exec` per SSH invocation and returns its
sanitized output. The platform caps command output and cancels the process when
the SSH session ends.
