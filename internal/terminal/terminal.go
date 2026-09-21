package terminal

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/term"
)

const (
	DefaultWidth  = 80
	DefaultHeight = 24
	OPEN_ALT      = "\x1b[?1049h"
	CLOSE_ALT     = "\x1b[?1049l"
	HIDE_CURSOR   = "\x1b[?25l"
	SHOW_CURSOR   = "\x1b[?25h"
	ENABLE_MOUSE  = "\x1b[?1000h\x1b[?1002h\x1b[?1006h"
	DISABLE_MOUSE = "\x1b[?1006l\x1b[?1002l\x1b[?1000l"
	CLEAR_SCREEN  = "\x1b[2J"
	MOVE_CURSOR   = "\x1b[H"
)

var ErrNotTerminal = errors.New("stdin is not a terminal")

type Terminal struct {
	mu       sync.Mutex
	oldstate *term.State
	fd       int
	W, H     int
}

func New(fd int) *Terminal { return &Terminal{fd: fd} }
func (t *Terminal) Enter() error {
	if !term.IsTerminal(t.fd) {
		return ErrNotTerminal
	}
	state, err := term.MakeRaw(t.fd)
	if err != nil {
		return err
	}
	_ = t.RefreshSize()
	t.mu.Lock()
	t.oldstate = state
	t.mu.Unlock()
	fmt.Print(HIDE_CURSOR, OPEN_ALT, ENABLE_MOUSE, MOVE_CURSOR, CLEAR_SCREEN)
	return nil
}
func (t *Terminal) Exit() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.oldstate == nil {
		return nil
	}
	fmt.Print(DISABLE_MOUSE, SHOW_CURSOR, CLOSE_ALT)
	err := term.Restore(t.fd, t.oldstate)
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
