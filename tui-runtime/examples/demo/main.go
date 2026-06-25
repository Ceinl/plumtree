// Command demo is a minimal native app that exercises the tui runtime and the
// default widget toolkit: a header, a body, and a row of two buttons laid out
// with Div containers.
//
//	go run ./examples/demo
//
// Tab / Shift+Tab move focus between buttons, Enter activates the focused one,
// the mouse clicks them directly, and q or Ctrl+C quits.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Ceinl/plumtree/tui-runtime/app"
	"github.com/Ceinl/plumtree/tui-runtime/components"
	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
	"github.com/Ceinl/plumtree/tui-runtime/layout"
)

func grow() layout.Unit        { return layout.Unit{Type: layout.UnitGrow} }
func px(v float64) layout.Unit { return layout.Unit{Type: layout.UnitPx, Value: v} }

func main() {
	status := components.NewText("Tab to move focus · Enter or click to activate · q quits")
	status.SetAlign(components.AlignCenter)

	clicks := 0
	count := components.NewText("clicks: 0")
	count.SetAlign(components.AlignCenter)

	ok := components.NewButton("  OK  ")
	cancel := components.NewButton("Cancel")
	buttons := []*components.Button{ok, cancel}
	focus := 0
	buttons[focus].SetFocused(true)

	ok.OnClick = func() {
		clicks++
		count.SetContent(fmt.Sprintf("clicks: %d", clicks))
	}
	cancel.OnClick = func() {
		clicks = 0
		count.SetContent("clicks: 0")
	}

	// Header bar.
	header := components.NewDiv()
	header.SetSize(grow(), px(1))
	header.SetStyle(bg(34, 31, 43))
	header.AppendChild(status)

	// Centered click counter.
	body := components.NewDiv()
	body.SetSize(grow(), grow())
	body.SetStyle(bg(22, 20, 27))
	body.JustifyContent(layout.JCenter)
	body.AlignItems(layout.ACenter)
	body.AppendChild(count)

	// Button row, right-aligned.
	row := components.NewDiv()
	row.SetSize(grow(), px(3))
	row.SetDirection(layout.Row)
	row.JustifyContent(layout.JRight)
	row.AlignItems(layout.ACenter)
	row.SetStyle(bg(28, 26, 36))
	okWrap := wrap(ok, 10)
	cancelWrap := wrap(cancel, 10)
	row.AppendChild(okWrap)
	row.AppendChild(cancelWrap)

	root := components.NewDiv()
	root.SetDirection(layout.Column)
	root.SetSize(grow(), grow())
	root.SetStyle(bg(22, 20, 27))
	root.AppendChild(header)
	root.AppendChild(body)
	root.AppendChild(row)

	setFocus := func(i int) {
		buttons[focus].SetFocused(false)
		focus = (i + len(buttons)) % len(buttons)
		buttons[focus].SetFocused(true)
	}

	a := app.New(root)
	a.OnKey = func(ev keyboard.Event) bool {
		switch {
		case ev.Type == keyboard.KeyCtrlC,
			ev.Type == keyboard.KeyRune && (ev.Ch == 'q' || ev.Ch == 'Q'):
			return true
		case ev.Type == keyboard.KeyTab && ev.Shift:
			setFocus(focus - 1)
		case ev.Type == keyboard.KeyTab:
			setFocus(focus + 1)
		case ev.Type == keyboard.KeyEnter:
			buttons[focus].Activate()
		case ev.Type == keyboard.KeyMouseLeftDown:
			for i, b := range buttons {
				if b.HandleMouseDown(ev.MouseX, ev.MouseY) {
					setFocus(i)
					break
				}
			}
		case ev.Type == keyboard.KeyMouseLeftUp:
			for _, b := range buttons {
				if b.HandleMouseUp(ev.MouseX, ev.MouseY) {
					break
				}
			}
		}
		return false
	}

	if err := a.Run(context.Background()); err != nil && err != context.Canceled {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

// wrap puts a fixed-width box around a button so the row can size it.
func wrap(b *components.Button, width float64) *components.Div {
	d := components.NewDiv()
	d.SetSize(px(width), px(3))
	d.AppendChild(b)
	return d
}

func bg(r, g, bl uint8) layout.Style {
	var s layout.Style
	s.SetBackground(r, g, bl)
	s.SetForeground(232, 229, 241)
	return s
}
