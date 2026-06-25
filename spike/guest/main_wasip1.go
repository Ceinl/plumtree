//go:build wasip1

// Command counter is the Plumtree feasibility-spike guest: a minimal counter
// TUI compiled to a WASI reactor (GOOS=wasip1 GOARCH=wasm -buildmode=c-shared)
// and driven by the host one frame at a time.
//
// It is a *guest*: it never touches a terminal, never emits ANSI, and never
// flushes. Each frame it decodes a host-supplied input event, mutates its
// state, builds a component tree with the real plumtree-tui runtime, lays it
// out, renders it into an in-memory cell buffer, and returns that buffer to the
// host as a structured abi.Frame across guest linear memory.
//
// Exported ABI (all via //go:wasmexport):
//
//	alloc(n)            -> ptr        host-writable scratch buffer of n bytes
//	free(ptr)                         release a buffer returned by alloc
//	frame(w,h,ptr,len)  -> ptr<<32|len   process one event, return a frame
package main

import (
	"fmt"
	"unsafe"

	"github.com/Ceinl/plumtree/tui-runtime/components"
	"github.com/Ceinl/plumtree/tui-runtime/layout"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
	"github.com/Ceinl/plumtree/spike/abi"
)

func main() {} // reactor: the host calls exports; main never drives the loop.

// state is the entire app state: a single integer.
type state struct {
	n    int
	quit bool
}

var st = &state{}

// keep pins host-visible buffers so Go's GC cannot reclaim or move them while
// the host reads/writes the corresponding linear-memory range. A real runtime
// would use a bump allocator; this map is the spike's simplest correct choice.
var keep = map[int32][]byte{}

//go:wasmexport alloc
func alloc(n int32) int32 {
	if n <= 0 {
		return 0
	}
	b := make([]byte, n)
	p := int32(uintptr(unsafe.Pointer(&b[0])))
	keep[p] = b
	return p
}

//go:wasmexport free
func free(p int32) { delete(keep, p) }

// writeOut copies data into a freshly allocated, pinned buffer and returns its
// linear-memory offset.
func writeOut(data []byte) int32 {
	p := alloc(int32(len(data)))
	copy(keep[p], data)
	return p
}

//go:wasmexport frame
func frame(w, h, evPtr, evLen int32) int64 {
	if evLen > 0 {
		evb := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(evPtr))), evLen)
		if ev, err := abi.DecodeEvent(evb); err == nil {
			st.update(ev)
		}
	}
	out := abi.EncodeFrame(st.render(int(w), int(h)))
	p := writeOut(out)
	return (int64(p) << 32) | int64(len(out))
}

// update applies one input event to the state.
func (s *state) update(ev abi.Event) {
	if ev.Kind != abi.KindKey {
		return
	}
	switch ev.Key {
	case abi.KeyArrowUp:
		s.n++
	case abi.KeyArrowDown:
		s.n--
	case abi.KeyCtrlC:
		s.quit = true
	case abi.KeyRune:
		switch ev.Ch {
		case 'q':
			s.quit = true
		case '+', 'k':
			s.n++
		case '-', 'j':
			s.n--
		case 'b':
			// Deliberate runaway: an unbounded compute loop with a back-edge.
			// Used to prove the host can cancel a busy guest via its per-frame
			// deadline. Never returns.
			busyLoop()
		}
	}
}

func busyLoop() {
	x := 0
	for i := 0; ; i++ {
		x += i
		_ = x
	}
}

// render builds the component tree for the current state and rasterizes it into
// a structured frame. The screen is never flushed — only snapshotted.
func (s *state) render(w, h int) abi.Frame {
	root := s.view()
	root.Layout(0, 0, w, h)
	scr := screen.NewScreen(w, h)
	root.Render(scr)
	return toFrame(scr, s.quit)
}

// view assembles the counter UI: a centered column with a title, the count, and
// a hint line. Mirrors the SDK app shape from PLATFORM_SPEC.md.
func (s *state) view() layout.Component {
	grow := layout.Unit{Type: layout.UnitGrow}

	var rootStyle layout.Style
	rootStyle.SetBackground(25, 23, 29)
	rootStyle.SetForeground(200, 200, 200)

	root := components.NewDiv()
	root.SetDirection(layout.Column)
	root.JustifyContent(layout.JCenter)
	root.AlignItems(layout.ACenter)
	root.SetSize(grow, grow)
	root.SetStyle(rootStyle)

	title := components.NewText("Plumtree counter")
	var titleStyle layout.Style
	titleStyle.SetBackground(25, 23, 29)
	titleStyle.SetForeground(120, 200, 255)
	titleStyle.AddTextDecoration(layout.Bold)
	title.SetStyle(titleStyle)
	title.SetAlign(components.AlignCenter)

	count := components.NewText(fmt.Sprintf("Count: %d", s.n))
	count.SetAlign(components.AlignCenter)

	hint := components.NewText("(↑/↓ change · q quits)")
	hint.SetAlign(components.AlignCenter)

	root.AppendChild(title)
	root.AppendChild(count)
	root.AppendChild(hint)
	return root
}

// toFrame converts a rendered cell buffer into a structured ABI frame, parsing
// the runtime's SGR color/decoration strings into structured values so no raw
// escape ever reaches the wire.
func toFrame(scr *screen.Screen, quit bool) abi.Frame {
	grid := scr.Snapshot()
	h := len(grid)
	w := 0
	if h > 0 {
		w = len(grid[0])
	}
	f := abi.Frame{W: w, H: h, Quit: quit, Cells: make([]abi.Cell, 0, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sc := grid[y][x]
			fg, ok := abi.ParseSGRColor(sc.Fg)
			if !ok {
				fg = abi.RGB{R: 200, G: 200, B: 200}
			}
			bg, ok := abi.ParseSGRColor(sc.Bg)
			if !ok {
				bg = abi.RGB{R: 25, G: 23, B: 29}
			}
			f.Cells = append(f.Cells, abi.Cell{
				Ch:    sc.Ch,
				Fg:    fg,
				Bg:    bg,
				Decor: abi.ParseSGRDecor(sc.Decor),
			})
		}
	}
	return f
}
