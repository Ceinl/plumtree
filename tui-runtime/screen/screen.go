package screen

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const DefaultBg = "\x1b[48;2;25;23;29m"
const DefaultFg = "\x1b[38;2;200;200;200m"
const DefaultDecor = ""

type Cell struct {
	Ch    rune
	Bg    string
	Fg    string
	Decor string
}

type Screen struct {
	w, h int
	old  [][]Cell
	cur  [][]Cell
	out  io.Writer
}

func NewScreen(w, h int) *Screen {
	return NewScreenWithOutput(w, h, os.Stdout)
}

// NewScreenWithOutput is like NewScreen but flushes to out instead of stdout.
// Hosted and SSH sessions render to a network connection, not the local TTY.
func NewScreenWithOutput(w, h int, out io.Writer) *Screen {
	s := &Screen{
		w:   w,
		h:   h,
		out: out,
	}
	s.resize(w, h)
	return s
}

func (s *Screen) resize(w, h int) {
	s.w, s.h = w, h
	s.old = make([][]Cell, h)
	s.cur = make([][]Cell, h)
	for i := 0; i < h; i++ {
		s.old[i] = make([]Cell, w)
		s.cur[i] = make([]Cell, w)
		for j := 0; j < w; j++ {
			s.old[i][j] = Cell{}
			s.cur[i][j] = Cell{Ch: ' ', Bg: DefaultBg, Fg: DefaultFg}
		}
	}
}

// Resize changes the screen dimensions and forces a full repaint on the next
// Flush. Existing content is discarded; callers should re-layout and re-render
// the component tree afterwards. Typically driven by a SIGWINCH handler.
func (s *Screen) Resize(w, h int) {
	fmt.Fprint(s.out, "\x1b[2J")
	s.resize(w, h)
}

func (s *Screen) Width() int  { return s.w }
func (s *Screen) Height() int { return s.h }

// Snapshot returns a deep copy of the current cell grid (h rows of w cells,
// row-major). It lets a host or serializer read what the component tree
// rendered without going through Flush — the basis for hosted mode, where the
// guest produces a structured frame and the host owns terminal output. The
// returned grid is owned by the caller and never aliases internal state.
func (s *Screen) Snapshot() [][]Cell {
	out := make([][]Cell, s.h)
	for y := 0; y < s.h; y++ {
		out[y] = append([]Cell(nil), s.cur[y]...)
	}
	return out
}

func (s *Screen) Clear() {
	for i := 0; i < s.h; i++ {
		for j := 0; j < s.w; j++ {
			s.cur[i][j] = Cell{Ch: ' ', Bg: DefaultBg, Fg: DefaultFg}
		}
	}
}

func (s *Screen) Set(x, y int, ch rune, fg, bg, decor string) {
	if fg == "" {
		fg = DefaultFg
	}
	if bg == "" {
		bg = DefaultBg
	}
	if decor == "" {
		decor = DefaultDecor
	}
	if x < 0 || x >= s.w || y < 0 || y >= s.h {
		return
	}
	s.cur[y][x] = Cell{Ch: ch, Bg: bg, Fg: fg, Decor: decor}
}

func (s *Screen) Flush() {
	// Hide the cursor and disable autowrap (DECAWM) for the duration of the
	// frame. With autowrap on — the terminal default — writing the cell in the
	// last column of the last row makes the terminal scroll up one line, which
	// shifts the managed region out from under the diff and shows up as the
	// whole screen flickering/creeping. Disabling autowrap lets us paint the
	// bottom-right cell in place; we restore it before returning.
	fmt.Fprint(s.out, "\x1b[?25l\x1b[?7l")
	for y := 0; y < s.h; y++ {
		changed := false
		startX := -1
		for x := 0; x < s.w; x++ {
			if s.cur[y][x] != s.old[y][x] {
				if !changed {
					changed = true
					startX = x
				}
			} else if changed {
				s.flushSegment(y, startX, x)
				changed = false
			}
		}
		if changed {
			s.flushSegment(y, startX, s.w)
		}
		copy(s.old[y], s.cur[y])
	}
	fmt.Fprint(s.out, "\x1b[0m\x1b[?7h")
}

func (s *Screen) flushSegment(y, startX, endX int) {
	fmt.Fprintf(s.out, "\x1b[%d;%dH", y+1, startX+1)

	var (
		b           strings.Builder
		activeFg    string
		activeBg    string
		activeDecor string
	)

	for x := startX; x < endX; x++ {
		cell := s.cur[y][x]

		if cell.Fg != activeFg || cell.Bg != activeBg || cell.Decor != activeDecor {
			if b.Len() > 0 {
				fmt.Fprint(s.out, b.String())
				b.Reset()
			}
			fmt.Fprint(s.out, "\x1b[0m")
			if cell.Decor != "" {
				fmt.Fprint(s.out, cell.Decor)
			}
			fmt.Fprint(s.out, cell.Bg, cell.Fg)

			activeFg = cell.Fg
			activeBg = cell.Bg
			activeDecor = cell.Decor
		}
		b.WriteRune(cell.Ch)
	}
	if b.Len() > 0 {
		fmt.Fprint(s.out, b.String())
	}
}

func (s *Screen) SetCursor(x, y int) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= s.w {
		x = s.w - 1
	}
	if y >= s.h {
		y = s.h - 1
	}
	fmt.Fprintf(s.out, "\x1b[%d;%dH", y+1, x+1)
}

func (s *Screen) ShowCursor() {
	fmt.Fprint(s.out, "\x1b[?25h")
}
