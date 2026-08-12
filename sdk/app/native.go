//go:build !wasip1

package app

import (
	"fmt"
	"os"

	"github.com/Ceinl/plumtree/sdk/internal/tui/keyboard"
)

func runPlatform(runtime *Runtime) error {
	defer runtime.Stop()
	if runtime.Stopped() {
		return runtime.Err()
	}
	_, _ = fmt.Fprintln(os.Stdout, runtime.Frame().Text())
	for event := range keyboard.Listen(runtime.Context()) {
		converted := platformEvent(event)
		if converted == nil {
			continue
		}
		if err := runtime.Dispatch(converted); err != nil {
			return err
		}
		if runtime.QuitRequested() {
			return runtime.Err()
		}
		_, _ = fmt.Fprintln(os.Stdout, runtime.Frame().Text())
	}
	return runtime.Err()
}

func platformEvent(event keyboard.Event) Event {
	switch event.Type {
	case keyboard.KeyRune:
		return KeyEvent{Key: Key(event.Ch), Shift: event.Shift, Ctrl: event.Ctrl, Alt: event.Alt, Cmd: event.Cmd}
	case keyboard.KeyEnter:
		return KeyEvent{Key: KeyEnter, Shift: event.Shift, Ctrl: event.Ctrl, Alt: event.Alt, Cmd: event.Cmd}
	case keyboard.KeyEscape:
		return KeyEvent{Key: KeyEscape, Shift: event.Shift, Ctrl: event.Ctrl, Alt: event.Alt, Cmd: event.Cmd}
	case keyboard.KeyTab:
		return KeyEvent{Key: KeyTab, Shift: event.Shift, Ctrl: event.Ctrl, Alt: event.Alt, Cmd: event.Cmd}
	case keyboard.KeyCtrlC:
		return KeyEvent{Key: KeyCtrlC, Shift: event.Shift, Ctrl: event.Ctrl, Alt: event.Alt, Cmd: event.Cmd}
	case keyboard.KeyArrowUp:
		return KeyEvent{Key: KeyUp}
	case keyboard.KeyArrowDown:
		return KeyEvent{Key: KeyDown}
	case keyboard.KeyArrowLeft:
		return KeyEvent{Key: KeyLeft}
	case keyboard.KeyArrowRight:
		return KeyEvent{Key: KeyRight}
	case keyboard.KeyPaste:
		return PasteEvent{Text: event.Text}
	case keyboard.KeyMouseLeftDown:
		return MouseEvent{X: event.MouseX, Y: event.MouseY, Button: MouseLeft, Action: MouseDown}
	case keyboard.KeyMouseLeftUp:
		return MouseEvent{X: event.MouseX, Y: event.MouseY, Button: MouseLeft, Action: MouseUp}
	case keyboard.KeyMouseLeftDrag:
		return MouseEvent{X: event.MouseX, Y: event.MouseY, Button: MouseLeft, Action: MouseDrag}
	case keyboard.KeyMouseWheelUp:
		return MouseEvent{X: event.MouseX, Y: event.MouseY, Button: MouseNone, Action: MouseWheelUp}
	case keyboard.KeyMouseWheelDown:
		return MouseEvent{X: event.MouseX, Y: event.MouseY, Button: MouseNone, Action: MouseWheelDown}
	default:
		return nil
	}
}
