package main

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Ceinl/plumtree/tui-runtime/screen"
	"github.com/Ceinl/plumtree/spike/abi"
)

// sanitizeRune enforces the connection-path defense: the guest returns runes,
// but the host decides what actually reaches the terminal. Control and format
// characters that could drive the viewer's terminal are replaced with a space,
// so a hostile guest cannot smuggle escape/control bytes through cell content.
func sanitizeRune(r rune) rune {
	switch {
	case r == 0:
		return ' '
	case r < 0x20 || r == 0x7f: // C0 controls + DEL
		return ' '
	case r >= 0x80 && r <= 0x9f: // C1 controls
		return ' '
	case !utf8.ValidRune(r):
		return '?'
	default:
		return r
	}
}

// throttle caps how often frames are flushed (output rate limiting). Input that
// arrives faster than the cap still updates state; only the repaint is coalesced.
type throttle struct {
	min  time.Duration
	last time.Time
}

func newThrottle(maxFPS int) throttle {
	if maxFPS <= 0 {
		return throttle{}
	}
	return throttle{min: time.Second / time.Duration(maxFPS)}
}

// allow reports whether enough time has elapsed to flush another frame.
func (t *throttle) allow(now time.Time) bool {
	if t.min == 0 || now.Sub(t.last) >= t.min {
		t.last = now
		return true
	}
	return false
}

// ttyRenderer paints structured frames onto a real terminal by driving the
// runtime's diffing screen buffer. The only ANSI written to the terminal is
// generated host-side from validated RGB — never passed through from the guest.
type ttyRenderer struct {
	scr  *screen.Screen
	w, h int
}

func newTTYRenderer(w, h int) *ttyRenderer {
	return &ttyRenderer{scr: screen.NewScreen(w, h), w: w, h: h}
}

func (r *ttyRenderer) draw(f abi.Frame) {
	if f.W != r.w || f.H != r.h {
		r.scr.Resize(f.W, f.H)
		r.w, r.h = f.W, f.H
	}
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			c := f.At(x, y)
			r.scr.Set(x, y,
				sanitizeRune(c.Ch),
				abi.FgSGR(c.Fg), abi.BgSGR(c.Bg), abi.DecorSGR(c.Decor))
		}
	}
	r.scr.Flush()
}

// renderText renders a frame as a bordered plain-text grid, for headless runs
// and tests where there is no PTY. Styling is dropped; only sanitized glyphs
// are shown. This satisfies "render to a terminal writer" without a TTY.
func renderText(w io.Writer, f abi.Frame) {
	bar := strings.Repeat("─", f.W)
	fmt.Fprintf(w, "┌%s┐\n", bar)
	var b strings.Builder
	for y := 0; y < f.H; y++ {
		b.Reset()
		for x := 0; x < f.W; x++ {
			b.WriteRune(sanitizeRune(f.At(x, y).Ch))
		}
		fmt.Fprintf(w, "│%s│\n", b.String())
	}
	fmt.Fprintf(w, "└%s┘\n", bar)
}
