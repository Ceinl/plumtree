//go:build wasip1

package app

import (
	"context"
	"os"
	"runtime"
	"unsafe"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/cli"
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

		encoded = rt.appendFrame(encoded[:0], &converter)
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

// cleanKeys maps ABI key types to clean SDK keys. A package-level table: it is
// read-only, so non-rune key events avoid rebuilding a map per keypress.
var cleanKeys = map[abi.KeyType]Key{
	abi.KeyArrowUp: KeyUp, abi.KeyArrowDown: KeyDown,
	abi.KeyArrowLeft: KeyLeft, abi.KeyArrowRight: KeyRight,
	abi.KeyEnter: KeyEnter, abi.KeyEscape: KeyEscape, abi.KeyTab: KeyTab,
	abi.KeyBackspace: KeyBackspace, abi.KeyDelete: KeyDelete,
	abi.KeyHome: KeyHome, abi.KeyEnd: KeyEnd,
	abi.KeyPageUp: KeyPageUp, abi.KeyPageDown: KeyPageDown,
	abi.KeyCtrlC: KeyCtrlC,
}

func cleanKey(key abi.KeyType) (Key, bool) {
	value, ok := cleanKeys[key]
	return value, ok
}

type TimerEvent struct{ ID uint32 }
