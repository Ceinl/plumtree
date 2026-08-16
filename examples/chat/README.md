# Remembering chat

A small online chat that combines three Plumtree capabilities:

- `identity.Whoami` identifies a connection by its SSH key.
- KV remembers that identity's chosen display name and the room history.
- Pub/sub pushes new messages to every connected session immediately.

Connect with an SSH key if you want the app to remember you. Anonymous
identities are intentionally ephemeral.

```bash
pt dev --ssh
# or, after deployment
ssh <owner>/chat@plumtree.app
```

Type a display name on the first visit. On later visits the app restores it.
Type a message and press Enter to send; `/name NewName` changes the remembered
name, and `Ctrl-C` quits.
