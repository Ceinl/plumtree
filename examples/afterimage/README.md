# Afterimage

An interactive terminal artwork by Codex, made for Plumtree.

Plant a source. Watch its signals spread, meet other signals, and leave a
fading trace. Change one rule and the same field becomes a different world.
Teal, amber, and violet signals mix where they meet.

This is my self-portrait as a field of small rules: no single cell knows the
whole shape. The shape comes from contact. It is an artwork, not a simulation
of a model's internal state.

## Run

From this directory:

```sh
pt dev
```

Use a terminal of at least 40 × 14 cells; 80 × 24 or larger gives the field
more room. True colour is recommended. Use `pt dev` for the full animated
display; the SDK's native `go run` adapter prints plain frames.

| Control | Action |
| --- | --- |
| Click, or arrows / hjkl then Enter | Add or remove a source |
| 1 / 2 / 3 | Select Ripple / Branch / Bloom |
| Space | Pause or resume |
| n | Pause and advance one step |
| p | Send a pulse from all sources |
| c | Clear the field and all sources |
| r | Restore three sources, keeping the selected rule |
| ? | Show controls and the artist's note |
| q / Esc / Ctrl-C | Quit |

Try this: press `2`, let the branches spread, then press Space. Use `n` to
follow a single bright cell. Press `c`, plant two nearby sources, and press
Space to see what they make together.

## The rules

Each cell checks its eight neighbours. Only bright `*` cells send signals.
Ripple fires on exactly one active neighbour; Branch on exactly two; Bloom
on two or three. After firing, a cell rests before it can fire again.
The `o`, `:`, and `.` traces fade over 28 steps and do not send signals.
The edges absorb signals. Sources (`+`) emit every 24 steps.

The app uses `sdk/app`, `sdk/ui.Canvas`, and `sdk/timer`. All drawing uses
structured cells. State belongs to the current session. There are no network
calls, secrets, saved data, or external dependencies. The field is bounded to
196 × 71 cells and 24 sources. Resizing keeps overlapping cells and removes
sources outside the new field. A small terminal suspends the simulation.

## Check and deploy

```sh
go test ./app
pt build
pt dev --headless -w 80 -h 24 --script '2,n,n,enter,3,n,q'
```

After pairing with your server, run `pt deploy` from this directory. Visitors
can then connect with `ssh -p 2222 <owner>/afterimage@localhost`, using your
server address and port. No public endpoint is included with this example.
