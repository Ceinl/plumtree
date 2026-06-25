//go:build !wasip1

package sdk

import (
	"context"
	"time"

	"github.com/Ceinl/plumtree/tui-runtime/app"
	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
	"github.com/Ceinl/plumtree/tui-runtime/layout"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
)

// RunTUI runs a TUI model against the local terminal. This native build drives
// the plumtree-tui runtime loop directly (raw mode, live input, resize), so
// authors can `go run .` their app. The hosted build (GOOS=wasip1) replaces
// this with the WASM-guest ABI loop; app code is unchanged.
func RunTUI(m Model, _ Meta) {
	a := app.New(&modelRoot{m: m})
	a.OnKey = func(ev keyboard.Event) bool {
		if e, ok := eventFromKeyboard(ev); ok {
			m.Update(e)
		}
		return quitRequested
	}
	a.OnResize = func(w, h int) { m.Update(ResizeMsg{W: w, H: h}) }
	// Drain process-local bus messages on a tick so a native publish reaches
	// Model.Update and triggers a repaint, mirroring the hosted push delivery.
	a.TickInterval = 50 * time.Millisecond
	a.OnTick = func() (render bool) {
		render = drainBus(m)
		return render || quitRequested
	}
	_ = a.Run(context.Background())
}

// modelRoot adapts a Model to a layout.Component: each frame the runtime calls
// Layout (where we rebuild the view from current state) then Render.
type modelRoot struct {
	m   Model
	cur layout.Component
}

func (r *modelRoot) GetStyle() layout.Style     { return layout.Style{} }
func (r *modelRoot) IsDirty() bool              { return true }
func (r *modelRoot) MakeDirty()                 {}
func (r *modelRoot) ClearDirty()                {}
func (r *modelRoot) SetParent(layout.Component) {}
func (r *modelRoot) Layout(x, y, w, h int) {
	r.cur = r.m.View()
	if r.cur != nil {
		r.cur.Layout(x, y, w, h)
	}
}
func (r *modelRoot) Render(s *screen.Screen) {
	if r.cur != nil {
		r.cur.Render(s)
	}
}

// eventFromKeyboard maps a runtime keyboard event to an SDK event.
func eventFromKeyboard(ev keyboard.Event) (Event, bool) {
	m := KeyMsg{Shift: ev.Shift, Ctrl: ev.Ctrl, Alt: ev.Alt, Cmd: ev.Cmd}
	if ev.Type == keyboard.KeyRune {
		m.Key = Key(ev.Ch)
		return m, true
	}
	key, ok := nativeKeyMap[ev.Type]
	if !ok {
		return nil, false
	}
	m.Key = key
	return m, true
}

var nativeKeyMap = map[keyboard.EventType]Key{
	keyboard.KeyArrowUp:    KeyUp,
	keyboard.KeyArrowDown:  KeyDown,
	keyboard.KeyArrowLeft:  KeyLeft,
	keyboard.KeyArrowRight: KeyRight,
	keyboard.KeyEnter:      KeyEnter,
	keyboard.KeyEscape:     KeyEsc,
	keyboard.KeyTab:        KeyTab,
	keyboard.KeyBackspace:  KeyBackspace,
	keyboard.KeyDelete:     KeyDelete,
	keyboard.KeyHome:       KeyHome,
	keyboard.KeyEnd:        KeyEnd,
	keyboard.KeyPageUp:     KeyPageUp,
	keyboard.KeyPageDown:   KeyPageDown,
	keyboard.KeyCtrlC:      KeyCtrlC,
}
