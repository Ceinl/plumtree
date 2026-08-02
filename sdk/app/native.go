//go:build !wasip1

package app

import (
	"context"
	"fmt"
	"os"

	"github.com/Ceinl/plumtree/sdk/cli"
	"github.com/Ceinl/plumtree/sdk/internal/tui/keyboard"
)

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

func runPlatform(runtime *Runtime) {
	if runtime.stopped {
		return
	}
	fmt.Fprintln(os.Stdout, runtime.Frame().Text())
	for event := range keyboard.Listen(runtime.ctx) {
		if err := runtime.Dispatch(platformEvent(event)); err != nil {
			return
		}
		if runtime.QuitRequested() {
			return
		}
		fmt.Fprintln(os.Stdout, runtime.Frame().Text())
	}
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
		return KeyEvent{Key: Key(event.Ch), Shift: event.Shift, Ctrl: event.Ctrl, Alt: event.Alt, Cmd: event.Cmd}
	}
}
