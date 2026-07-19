//go:build wasip1

package sdk

import (
	"runtime"
	"unsafe"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/tui-runtime/layout"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
)

// Host functions imported by every hosted Plumtree guest. The guest drives its
// own loop: recv asks the host for the next input event (blocking), present
// hands the host a rendered frame. Command mode means main() runs, so the
// author's `func main(){ sdk.RunTUI(...) }` works unchanged.

//go:wasmimport plumtree recv
func hostRecv(ptr, capBytes int32) int32

//go:wasmimport plumtree present
func hostPresent(ptr, length int32)

// evbuf is the fixed, always-live buffer the host writes events into. It must be
// large enough for the biggest event: a KindMessage carries the inline topic and
// payload, so size it past abi.EventLen + BusMaxTopic + BusMaxData.
var evbuf [4096]byte

// RunTUI runs the model's event loop against the host. It returns when the host
// signals stop (recv < 0) or the model calls Quit.
func RunTUI(m Model, _ Meta) {
	w, h := 0, 0
	var previous layout.Component
	var mouseDispatch mouseDispatcher
	var scr *screen.Screen
	var encoded []byte
	converter := newFrameConverter()
	evPtr := int32(uintptr(unsafe.Pointer(&evbuf[0])))

	for {
		n := hostRecv(evPtr, int32(len(evbuf)))
		if n < 0 {
			return // host asked us to stop (disconnect / scripted end)
		}
		if n > 0 {
			if e, err := abi.DecodeEvent(evbuf[:n]); err == nil {
				if e.Kind == abi.KindResize {
					w, h = e.W, e.H
				}
				if sev, ok := eventFromABI(e); ok {
					if mouse, ok := sev.(MouseMsg); ok {
						if handler, ok := previous.(layout.MouseHandler); ok {
							mouseDispatch.Dispatch(handler, mouse)
						} else {
							mouseDispatch.Dispatch(nil, mouse)
						}
					}
					m.Update(sev)
				}
			}
		}

		if scr == nil || scr.Width() != w || scr.Height() != h {
			scr = screen.NewScreen(w, h)
		} else {
			scr.Clear()
		}
		previous = m.View()
		if previous != nil && w > 0 && h > 0 {
			previous.Layout(0, 0, w, h)
			previous.Render(scr)
		}
		encoded = abi.AppendFrame(encoded[:0], converter.frame(scr, quitRequested))
		hostPresent(int32(uintptr(unsafe.Pointer(&encoded[0]))), int32(len(encoded)))
		runtime.KeepAlive(encoded)

		if quitRequested {
			return
		}
	}
}

// eventFromABI maps a host ABI event to an SDK event.
func eventFromABI(e abi.Event) (Event, bool) {
	switch e.Kind {
	case abi.KindResize:
		return ResizeMsg{W: e.W, H: e.H}, true
	case abi.KindMessage:
		return MessageMsg{Topic: e.Topic, Data: e.Data}, true
	case abi.KindMouse:
		return MouseMsg{X: e.MouseX, Y: e.MouseY, Button: MouseButton(e.Button), Action: MouseAction(e.Action)}, true
	case abi.KindKey:
		// handled below
	default:
		return nil, false
	}

	m := KeyMsg{
		Shift: e.Mods&abi.ModShift != 0,
		Ctrl:  e.Mods&abi.ModCtrl != 0,
		Alt:   e.Mods&abi.ModAlt != 0,
		Cmd:   e.Mods&abi.ModCmd != 0,
	}
	if e.Key == abi.KeyRune {
		m.Key = Key(e.Ch)
		return m, true
	}
	key, ok := abiKeyMap[e.Key]
	if !ok {
		return nil, false
	}
	m.Key = key
	return m, true
}

var abiKeyMap = map[abi.KeyType]Key{
	abi.KeyArrowUp:    KeyUp,
	abi.KeyArrowDown:  KeyDown,
	abi.KeyArrowLeft:  KeyLeft,
	abi.KeyArrowRight: KeyRight,
	abi.KeyEnter:      KeyEnter,
	abi.KeyEscape:     KeyEsc,
	abi.KeyTab:        KeyTab,
	abi.KeyBackspace:  KeyBackspace,
	abi.KeyDelete:     KeyDelete,
	abi.KeyHome:       KeyHome,
	abi.KeyEnd:        KeyEnd,
	abi.KeyPageUp:     KeyPageUp,
	abi.KeyPageDown:   KeyPageDown,
	abi.KeyCtrlC:      KeyCtrlC,
}

// toFrame converts a rendered cell buffer into a structured ABI frame, parsing
// the runtime's SGR color/decoration strings into structured values so no raw
// escape reaches the wire.
type frameConverter struct {
	cells []abi.Cell
}

func newFrameConverter() *frameConverter {
	return &frameConverter{}
}

func (c *frameConverter) frame(scr *screen.Screen, quit bool) abi.Frame {
	w, h := scr.Width(), scr.Height()
	n := w * h
	if cap(c.cells) < n {
		c.cells = make([]abi.Cell, n)
	} else {
		c.cells = c.cells[:n]
	}
	index := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sc := scr.CellAt(x, y)
			c.cells[index] = abi.Cell{
				Ch:    sc.Ch,
				Fg:    frameColor(sc.Fg, abi.RGB{R: 200, G: 200, B: 200}),
				Bg:    frameColor(sc.Bg, abi.RGB{R: 25, G: 23, B: 29}),
				Decor: abi.ParseSGRDecor(sc.Decor),
			}
			index++
		}
	}
	return abi.Frame{W: w, H: h, Quit: quit, Cells: c.cells}
}

func frameColor(sgr string, fallback abi.RGB) abi.RGB {
	color, ok := abi.ParseSGRColor(sgr)
	if !ok {
		return fallback
	}
	return color
}
