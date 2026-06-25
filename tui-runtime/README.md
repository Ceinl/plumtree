# Plumtree TUI Runtime

Standalone Go TUI runtime extracted from `github.com/Ceinl/plums/internal/ui/tui`.

Owns:

- layout primitives.
- component tree rendering.
- a default widget toolkit (div, text, button).
- screen/cell buffer.
- diff calculation.
- native demo apps for runtime validation.

Does not own:

- Plumtree host capabilities.
- WASM ABI.
- deploy/runtime platform behavior.
- application widgets (chat log, editor, session list, …) — those stay in the host app.

## Install

```bash
go get github.com/Ceinl/plumtree/tui-runtime@latest
```

Requires Go 1.26+.

## Packages

| Import | Responsibility |
| --- | --- |
| `github.com/Ceinl/plumtree/tui-runtime/screen` | Cell buffer with diffed flushing — only changed cells are written to the terminal. |
| `github.com/Ceinl/plumtree/tui-runtime/layout` | Layout primitives (`Style`, `Unit`, `Padding`, `Direction`) and the `Component` interface every widget implements. |
| `github.com/Ceinl/plumtree/tui-runtime/components` | The default widget toolkit: `Div` (flexbox-style container with grow/percent sizing, justify/align), `Text` (wrapping + alignment, inherits parent style), and `Button` (focusable, clickable, normal/focused/pressed states). |
| `github.com/Ceinl/plumtree/tui-runtime/keyboard` | Terminal input parsing (CSI/SS3/UTF-8/mouse/bracketed paste/kitty + modifyOtherKeys) into `keyboard.Event`s over a channel. |
| `github.com/Ceinl/plumtree/tui-runtime/terminal` | Raw-mode entry/exit, alt screen, mouse/paste/extended-key toggles, size queries, and tmux extended-keys management. |
| `github.com/Ceinl/plumtree/tui-runtime/app` | The runtime loop: wires terminal + keyboard + screen and drives a root `layout.Component`, repainting on input, resize (SIGWINCH) and an optional tick. |

The toolkit widgets implement `layout.Component`; the runtime only renders the
tree and routes events. Build app-specific widgets the same way — implement
`layout.Component` — and mix them freely with the defaults.

## Usage

```go
grow := layout.Unit{Type: layout.UnitGrow}

root := components.NewDiv()
root.SetDirection(layout.Column)
root.SetSize(grow, grow)
root.AppendChild(components.NewText("Hello"))

ok := components.NewButton("OK")
ok.OnClick = func() { /* ... */ }
root.AppendChild(ok)

a := app.New(root)
a.OnKey = func(ev keyboard.Event) (quit bool) {
	switch ev.Type {
	case keyboard.KeyEnter:
		ok.Activate()
	case keyboard.KeyMouseLeftDown:
		ok.HandleMouseDown(ev.MouseX, ev.MouseY)
	case keyboard.KeyMouseLeftUp:
		ok.HandleMouseUp(ev.MouseX, ev.MouseY)
	}
	return ev.Type == keyboard.KeyCtrlC
}
if err := a.Run(context.Background()); err != nil {
	log.Fatal(err)
}
```

`App` also exposes `OnResize`, and `TickInterval`/`OnTick` for animation or
streaming output whose repaint rate is decoupled from keystrokes.

## Demo

```bash
go run ./examples/demo
```

A header, a centered click counter, and two buttons. Tab / Shift+Tab move
focus, Enter activates the focused button, the mouse clicks them directly, and
`q` or Ctrl+C quits.
