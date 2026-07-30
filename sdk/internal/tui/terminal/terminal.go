package terminal

import (
	"errors"
	"fmt"

	"golang.org/x/term"
)

const (
	DefaultWidth            = 80
	DefaultHeight           = 24
	OPEN_ALT                = "\x1b[?1049h"
	CLOSE_ALT               = "\x1b[?1049l"
	HIDE_CURSOR             = "\x1b[?25l"
	SHOW_CURSOR             = "\x1b[?25h"
	ENABLE_MOUSE            = "\x1b[?1000h\x1b[?1002h\x1b[?1006h"
	DISABLE_MOUSE           = "\x1b[?1006l\x1b[?1002l\x1b[?1000l"
	ENABLE_BRACKETED_PASTE  = "\x1b[?2004h"
	DISABLE_BRACKETED_PASTE = "\x1b[?2004l"
	// Ask compatible terminals to distinguish modified keys like Shift+Enter.
	ENABLE_KITTY_KEYBOARD     = "\x1b[>1u"
	DISABLE_KITTY_KEYBOARD    = "\x1b[<u"
	ENABLE_MODIFY_OTHER_KEYS  = "\x1b[>4;2m"
	DISABLE_MODIFY_OTHER_KEYS = "\x1b[>4;0m"
	CLEAR_SCREEN              = "\x1b[2J"
	MOVE_CURSOR               = "\x1b[H"
)

var ErrNotTerminal = errors.New("stdin is not a terminal")

type Terminal struct {
	oldstate *term.State
	fd       int
	W, H     int
}

func New(fd int) *Terminal {
	return &Terminal{
		fd: fd,
	}
}

func (t *Terminal) Enter() error {
	if !term.IsTerminal(t.fd) {
		return ErrNotTerminal
	}
	oldstate, err := term.MakeRaw(t.fd)
	if err != nil {
		return err
	}
	if err := t.RefreshSize(); err != nil {
		_ = term.Restore(t.fd, oldstate)
		return err
	}
	t.oldstate = oldstate
	fmt.Print(HIDE_CURSOR, OPEN_ALT, ENABLE_MOUSE, ENABLE_BRACKETED_PASTE, ENABLE_KITTY_KEYBOARD, ENABLE_MODIFY_OTHER_KEYS, MOVE_CURSOR, CLEAR_SCREEN)
	return nil
}

// Exit restores the terminal to the state captured by Enter: it emits the
// teardown escape sequences and restores the saved termios state. It is
// idempotent — calling it more than once (e.g. once from a signal handler and
// again via a deferred call) is a no-op after the first successful restore.
func (t *Terminal) Exit() error {
	if t.oldstate == nil {
		return nil
	}

	fmt.Print(DISABLE_MODIFY_OTHER_KEYS + DISABLE_KITTY_KEYBOARD + DISABLE_BRACKETED_PASTE + DISABLE_MOUSE + SHOW_CURSOR + CLOSE_ALT)
	err := term.Restore(t.fd, t.oldstate)
	// Clear oldstate unconditionally so a second call is a guaranteed no-op and
	// we never try to restore a stale state.
	t.oldstate = nil
	return err
}

func (t *Terminal) RefreshSize() error {
	w, h, err := term.GetSize(t.fd)
	if err != nil {
		if t.W < 1 {
			t.W = DefaultWidth
		}
		if t.H < 1 {
			t.H = DefaultHeight
		}
		return err
	}
	if w < 1 {
		w = DefaultWidth
	}
	if h < 1 {
		h = DefaultHeight
	}
	t.W, t.H = w, h
	return nil
}
