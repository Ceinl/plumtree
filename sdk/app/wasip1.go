//go:build wasip1

package app

import (
	"context"
	"os"
	"runtime"
	"unsafe"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/cli"
	"github.com/Ceinl/plumtree/sdk/ui"
)

// The clean app runtime uses the same bounded receive/present ABI as the
// legacy adapter. Keeping the imports in this package makes a clean guest
// independent of the old root sdk runtime and lets both hosted runners serve
// the same public lifecycle.
//
//go:wasmimport plumtree recv
func hostRecv(ptr, capBytes int32) int32

//go:wasmimport plumtree present
func hostPresent(ptr, length int32)

//go:wasmimport plumtree timer_start
func hostTimerStart(delayNanos int64, recurring int32) int32

//go:wasmimport plumtree timer_cancel
func hostTimerCancel(id int32) int32

//go:wasmimport plumtree goodbye_set
func hostGoodbyeSet(ptr, length int32)

func runCLIIfRequested(runtime *Runtime) bool {
	command, attached := runtime.Commands()
	if !attached || len(os.Args) <= 1 {
		return false
	}
	execution := cli.Execute(context.Background(), command, os.Args[1:], cli.Streams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr})
	if execution.ExitCode != 0 {
		os.Exit(execution.ExitCode)
	}
	return true
}

func runPlatform(rt *Runtime) error {
	defer rt.Stop()
	var eventBuffer [4096]byte
	var encoded []byte
	converter := cleanFrameConverter{}
	ids := make(map[uint32]Event)
	for _, spec := range rt.HostedTimers() {
		id := hostTimerStart(spec.Interval.Nanoseconds(), 1)
		if id > 0 {
			ids[uint32(id)] = spec.Event
		}
	}
	defer func() {
		for id := range ids {
			hostTimerCancel(int32(id))
		}
	}()
	if rt.QuitRequested() {
		emitGoodbye(rt)
		return rt.Err()
	}

	for {
		// Yield to the Go scheduler every iteration. GOOS=wasip1 has no
		// async preemption, and the steady-state loop below performs no
		// channel operations or blocking calls of its own: without this
		// handoff, GC workers and subscription goroutines can starve while
		// per-frame garbage (Clone, encoded frame) piles up until the
		// linear-memory cap kills the guest. See spark/counter flood repro.
		runtime.Gosched()
		n := hostRecv(int32(uintptr(unsafe.Pointer(&eventBuffer[0]))), int32(len(eventBuffer)))
		if n < 0 {
			return rt.Err()
		}
		if n > 0 {
			if event, err := abi.DecodeEvent(eventBuffer[:n]); err == nil {
				if mapped, ok := cleanEvent(event); ok {
					if timer, ok := mapped.(TimerEvent); ok {
						mapped = ids[timer.ID]
						if mapped == nil {
							continue
						}
					}
					if err := rt.Dispatch(mapped); err != nil {
						return err
					}
				}
			}
		}

		frame := converter.frame(rt.Frame(), rt.QuitRequested())
		encoded = abi.AppendFrame(encoded[:0], frame)
		hostPresent(int32(uintptr(unsafe.Pointer(&encoded[0]))), int32(len(encoded)))
		runtime.KeepAlive(encoded)
		if rt.QuitRequested() {
			emitGoodbye(rt)
			return rt.Err()
		}
	}
}

func emitGoodbye(rt *Runtime) {
	message := rt.Goodbye().Text
	if len(message) == 0 || len(message) > abi.GoodbyeMaxLen {
		return
	}
	value := []byte(message)
	hostGoodbyeSet(int32(uintptr(unsafe.Pointer(&value[0]))), int32(len(value)))
	runtime.KeepAlive(value)
}

func cleanEvent(event abi.Event) (Event, bool) {
	switch event.Kind {
	case abi.KindResize:
		return ResizeEvent{Width: event.W, Height: event.H}, true
	case abi.KindMessage:
		return MessageEvent{Topic: event.Topic, Data: append([]byte(nil), event.Data...)}, true
	case abi.KindMouse:
		return MouseEvent{X: event.MouseX, Y: event.MouseY, Button: MouseButton(event.Button), Action: MouseAction(event.Action)}, true
	case abi.KindTimer:
		return TimerEvent{ID: event.CommandID}, true
	case abi.KindKey:
		key := Key(event.Ch)
		if event.Key != abi.KeyRune {
			var ok bool
			key, ok = cleanKey(event.Key)
			if !ok {
				return nil, false
			}
		}
		return KeyEvent{Key: key, Shift: event.Mods&abi.ModShift != 0, Ctrl: event.Mods&abi.ModCtrl != 0, Alt: event.Mods&abi.ModAlt != 0, Cmd: event.Mods&abi.ModCmd != 0}, true
	default:
		return nil, false
	}
}

func cleanKey(key abi.KeyType) (Key, bool) {
	keys := map[abi.KeyType]Key{
		abi.KeyArrowUp: KeyUp, abi.KeyArrowDown: KeyDown,
		abi.KeyArrowLeft: KeyLeft, abi.KeyArrowRight: KeyRight,
		abi.KeyEnter: KeyEnter, abi.KeyEscape: KeyEscape, abi.KeyTab: KeyTab,
		abi.KeyBackspace: KeyBackspace, abi.KeyDelete: KeyDelete,
		abi.KeyHome: KeyHome, abi.KeyEnd: KeyEnd,
		abi.KeyPageUp: KeyPageUp, abi.KeyPageDown: KeyPageDown,
		abi.KeyCtrlC: KeyCtrlC,
	}
	value, ok := keys[key]
	return value, ok
}

type TimerEvent struct{ ID uint32 }

type cleanFrameConverter struct{ cells []abi.Cell }

func (converter *cleanFrameConverter) frame(frame ui.Frame, quit bool) abi.Frame {
	w, h := frame.Width(), frame.Height()
	n := w * h
	if cap(converter.cells) < n {
		converter.cells = make([]abi.Cell, n)
	} else {
		converter.cells = converter.cells[:n]
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cell, ok := frame.Cell(x, y)
			if !ok {
				continue
			}
			fg := cell.Style.Foreground
			bg := cell.Style.Background
			if !cell.Style.HasForeground {
				fg = ui.RGB(200, 200, 200)
			}
			if !cell.Style.HasBackground {
				bg = ui.RGB(25, 23, 29)
			}
			var decor uint8
			if cell.Style.Decorations&ui.Bold != 0 {
				decor |= abi.DecorBold
			}
			if cell.Style.Decorations&ui.Underline != 0 {
				decor |= abi.DecorUnderline
			}
			converter.cells[y*w+x] = abi.Cell{Ch: cell.Rune, Fg: abi.RGB{R: fg.R, G: fg.G, B: fg.B}, Bg: abi.RGB{R: bg.R, G: bg.G, B: bg.B}, Decor: decor}
		}
	}
	return abi.Frame{W: w, H: h, Quit: quit, Cells: converter.cells}
}
