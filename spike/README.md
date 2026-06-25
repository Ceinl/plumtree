# Plumtree WASM feasibility spike (Phase 1)

This spike retires the load-bearing technical risk from `PLATFORM_SPEC.md`:

> confirm the Plumtree runtime + a real TUI loop compile and run under
> `wasip1`/TinyGo in wazero, with input/render bridged through the host renderer.

**Result: feasible.** A counter TUI built on the real `plumtree-tui` runtime
compiles to `wasip1` WASM, runs in wazero with no ambient authority, receives
host input events, and returns structured frames the host renders — with
per-frame deadlines that reliably kill a runaway guest and no path for the guest
to emit raw terminal escapes.

## Layout

| Path | What |
| --- | --- |
| `abi/` | ABI v0 wire format (event host→guest, frame guest→host). Pure Go, shared. No raw ANSI on the wire — colors are structured RGB. |
| `guest/` | The counter app. Built `GOOS=wasip1 GOARCH=wasm -buildmode=c-shared`. Uses the real `plumtree-tui` layout/screen/components; exports `alloc`/`free`/`frame`. |
| `host/` | wazero runner: instantiates the guest with limits, drives the frame loop, sanitizes + renders. `headless` (text, TTY-free) and `tty` (interactive) modes. |
| `build.sh` | Builds guest WASM + host. Also builds a TinyGo guest if `tinygo` is present. |

## Run

```bash
./build.sh
./dist/host -headless -script "up,up,up,down,q"   # deterministic, no PTY
./dist/host                                        # interactive (needs a terminal)
go test ./...                                      # unit + end-to-end (loads the wasm)
```

Runaway-guest cancellation demo (the `b` token spins forever in the guest):

```bash
./dist/host -headless -frame-timeout 1s -script "up,b,q"
# ... ✗ guest exceeded per-frame deadline; session terminated
```

## The ABI (v0)

Host-driven loop. Each frame the host writes an event into guest linear memory,
calls the guest's exported `frame`, and reads back a serialized frame:

```
host                                   guest (wasip1 reactor)
 │  alloc(len(event)) ───────────────▶ │  make([]byte,n); return &b[0] offset
 │  mem.Write(ptr, eventBytes)         │
 │  frame(w, h, ptr, len) ───────────▶ │  decode event → update state
 │                                     │  build tree → Layout → Render → Snapshot
 │  ◀──────────── packed ptr<<32|len   │  encode frame → alloc/copy → return
 │  mem.Read(ptr, len); free(ptr)      │
 │  sanitize runes → render            │
```

* **Event** (host→guest): magic, version, kind, ABI-stable `KeyType`, rune,
  modifier bits.
* **Frame** (guest→host): magic, version, flags (`quit`), `w`/`h`, then `w*h`
  cells of `rune | fg RGB | bg RGB | decor-bits`. **No escape sequences.**

`KeyType` is independent of the runtime's `keyboard` enum, so the guest never
depends on host internals.

## What this proves (Phase 1 exit criteria)

- ✅ A counter TUI runs inside wazero and responds to key input
  (`up`/`down`/`+`/`-`, `q` quits) — driven entirely through the ABI.
- ✅ The guest cannot emit raw ANSI: the wire format has no escape field
  (colors are RGB), and the host sanitizes every rune (C0/C1/DEL → space)
  before it reaches a terminal. Covered by `TestRenderTextDropsGuestEscapes`.
- ✅ Host cancellation reliably stops a busy guest: a per-frame wall-clock
  deadline + wazero `WithCloseOnContextDone` aborts an infinite compute loop.
  Covered by `TestRunawayGuestIsCancelled`.
- ✅ Resource caps enforced: linear-memory page cap (`WithMemoryLimitPages`),
  guest stdout/stderr captured as logs (never the terminal), no FS/env/args/net.
- ✅ Limitations documented (here + `PLATFORM_SPEC.md` "Phase 1 findings").

## Go WASI vs TinyGo (measurements)

Measured on this machine (Go 1.26.2, wazero v1.12.0, darwin/arm64):

| Metric | Stock Go `wasip1` | TinyGo |
| --- | --- | --- |
| guest `.wasm` size | **2.61 MiB** | not measured (not installed) |
| min linear memory to instantiate | **39 pages (~2.5 MiB)** declared; ~48 pages (3 MiB) to run | expected far lower |
| cold process start (incl. wazero compile) | ~0.56 s total | not measured |

The 39-page memory floor and 2.6 MiB binary are the Go runtime's overhead;
TinyGo is expected to cut both substantially but historically trails the latest
Go on `wasip1`/reactor support — and this runtime requires **Go 1.26**. The
comparison is **deferred** until TinyGo is installed and its `wasip1` +
`-buildmode=c-shared` + `//go:wasmexport` support is confirmed against Go 1.26
language features. `build.sh` builds a TinyGo guest automatically when present.

## Known limitations / follow-ups

See the "Phase 1 findings" section added to `PLATFORM_SPEC.md`. Headlines:

1. **Color model.** `screen.Cell` stores colors as ANSI SGR *strings*. The guest
   parses them back to RGB at the ABI boundary; the real runtime should store
   structured color so no parsing/round-trip is needed.
2. **Allocator.** The guest's `alloc`/`free` use a `map[ptr][]byte`. Fine for the
   spike; a bump/arena allocator (or fixed double-buffer) is wanted for the runner.
3. **Per-frame model.** One synchronous `frame()` call per event. Apps that need
   their own timers/streaming will need a guest-initiated tick or async frames.
4. **Whole-buffer frames.** The guest returns the full grid each frame; the host
   diffs via `screen`. A guest-side diff (changed cells only) would cut ABI
   traffic for large screens.
5. **Unicode width.** Wide/zero-width/combining runes are passed through 1 cell
   wide; proper width handling is a runtime concern, unchanged by this spike.
