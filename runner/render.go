package runner

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Ceinl/plumtree/tui-runtime/screen"
	"github.com/Ceinl/plumtree/sdk/abi"
)

// sanitizeRune enforces the connection-path defense: the guest returns runes,
// but the host decides what reaches the terminal. Control and format characters
// that could drive the viewer's terminal become spaces, so a hostile guest
// cannot smuggle escape/control bytes through cell content.
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

// TextSink renders frames as bordered plain text to a writer. Used by
// `pt dev --headless` and tests, where there is no PTY. Styling is dropped;
// only sanitized glyphs are shown.
type TextSink struct{ W io.Writer }

func (s TextSink) Present(f abi.Frame) {
	bar := strings.Repeat("─", f.W)
	fmt.Fprintf(s.W, "┌%s┐\n", bar)
	var b strings.Builder
	for y := 0; y < f.H; y++ {
		b.Reset()
		for x := 0; x < f.W; x++ {
			b.WriteRune(sanitizeRune(f.At(x, y).Ch))
		}
		fmt.Fprintf(s.W, "│%s│\n", b.String())
	}
	fmt.Fprintf(s.W, "└%s┘\n", bar)
}

// TTYSink paints frames onto a real terminal via the runtime's diffing screen
// buffer. The only ANSI written is generated host-side from validated RGB —
// never passed through from the guest. A frame-rate cap coalesces repaints.
type TTYSink struct {
	scr   *screen.Screen
	w, h  int
	thr   throttle
	dirty bool
}

// NewTTYSink returns a sink sized to w x h that flushes to stdout, capped at
// maxFPS repaints/sec.
func NewTTYSink(w, h, maxFPS int) *TTYSink {
	return NewTTYSinkWriter(w, h, maxFPS, nil)
}

// NewTTYSinkWriter is like NewTTYSink but flushes to out (e.g. an SSH channel).
// A nil out renders to stdout.
func NewTTYSinkWriter(w, h, maxFPS int, out io.Writer) *TTYSink {
	scr := screen.NewScreen(w, h)
	if out != nil {
		scr = screen.NewScreenWithOutput(w, h, out)
	}
	return &TTYSink{scr: scr, w: w, h: h, thr: newThrottle(maxFPS)}
}

func (s *TTYSink) Present(f abi.Frame) {
	if f.W != s.w || f.H != s.h {
		s.scr.Resize(f.W, f.H)
		s.w, s.h = f.W, f.H
	}
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			c := f.At(x, y)
			s.scr.Set(x, y, sanitizeRune(c.Ch),
				fgSGR(c.Fg), bgSGR(c.Bg), abi.DecorSGR(c.Decor))
		}
	}
	if s.thr.allow(time.Now()) {
		s.scr.Flush()
	}
}

func fgSGR(c abi.RGB) string {
	if c == (abi.RGB{}) {
		return screen.DefaultFg
	}
	return abi.FgSGR(c)
}

func bgSGR(c abi.RGB) string {
	if c == (abi.RGB{}) {
		return screen.DefaultBg
	}
	return abi.BgSGR(c)
}

// throttle caps how often frames are flushed (output rate limiting).
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

func (t *throttle) allow(now time.Time) bool {
	if t.min == 0 || now.Sub(t.last) >= t.min {
		t.last = now
		return true
	}
	return false
}
