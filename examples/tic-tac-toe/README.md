# Multiplayer tic-tac-toe

A mouse-driven, shared Plumtree game:

- The first two distinct identities to connect receive X and O.
- Later connections are spectators with the same live board.
- KV compare-and-swap serializes simultaneous joins and moves.
- Pub/sub redraws every connected session after a change.
- Heartbeat leases reclaim seats after abrupt SSH disconnects.

```bash
pt dev --ssh
# or, after deployment
ssh <owner>/tic-tac-toe@plumtree.app
```

Click an empty square when it is your turn. After a win or draw, either player
can click the board to begin another round. Press `q`, `Esc`, or `Ctrl-C` to
disconnect. A graceful disconnect releases the seat immediately; otherwise it
becomes available after roughly twelve seconds.
