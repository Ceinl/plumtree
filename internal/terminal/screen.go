package terminal

import (
	"io"
	"os"
	"strconv"
	"unicode/utf8"

	"github.com/Ceinl/plumtree/sdk/abi"
)

const (
	DefaultBg = "\x1b[48;2;25;23;29m"
	DefaultFg = "\x1b[38;2;200;200;200m"
	MinWidth  = 1
	MaxWidth  = abi.MaxFrameWidth
	MinHeight = 1
	MaxHeight = abi.MaxFrameHeight
	MaxCells  = 150_000
)

// decorCodes maps decoration bits to their SGR reset-sequence codes. A package
// variable so Flush does not rebuild it on every style change.
var decorCodes = []struct {
	bit  uint8
	code byte
}{{abi.DecorBold, '1'}, {abi.DecorItalic, '3'}, {abi.DecorUnderline, '4'}}

type Screen struct {
	w, h     int
	old, cur [][]abi.Cell
	buffer   []byte
	out      io.Writer
	// failed reports whether the last Flush did not fully reach out. A failed
	// write leaves old stale, so the next changed-cell flush self-heals.
	failed bool
}

func NewScreen(w, h int) *Screen { return NewScreenWithOutput(w, h, os.Stdout) }
func NewScreenWithOutput(w, h int, out io.Writer) *Screen {
	s := &Screen{out: out}
	s.Resize(w, h)
	return s
}
func (s *Screen) Resize(w, h int) {
	if w < MinWidth {
		w = MinWidth
	}
	if w > MaxWidth {
		w = MaxWidth
	}
	if h < MinHeight {
		h = MinHeight
	}
	if h > MaxHeight {
		h = MaxHeight
	}
	if w > MaxCells/h {
		w = MaxCells / h
	}
	s.w, s.h = w, h
	s.old = make([][]abi.Cell, h)
	s.cur = make([][]abi.Cell, h)
	for y := range s.cur {
		s.old[y] = make([]abi.Cell, w)
		s.cur[y] = make([]abi.Cell, w)
		for x := range s.cur[y] {
			s.cur[y][x] = abi.Cell{Ch: ' '}
		}
	}
}

// Set stores numeric colors. ANSI is generated only for changed runs in Flush.
func (s *Screen) Set(x, y int, c abi.Cell) {
	if x < 0 || x >= s.w || y < 0 || y >= s.h {
		return
	}
	s.cur[y][x] = c
}

// SetRow copies one row of cells into the current grid. Cells beyond the
// screen width are dropped; a partial row leaves the rest untouched.
func (s *Screen) SetRow(y int, cells []abi.Cell) {
	if y < 0 || y >= s.h || len(cells) == 0 {
		return
	}
	if len(cells) > s.w {
		cells = cells[:s.w]
	}
	copy(s.cur[y], cells)
}

// Healthy reports whether the last Flush fully reached out. A frame identical
// to the last flushed one is a no-op only while the screen is healthy; a
// failed write leaves old stale and forces a repaint on the next flush.
func (s *Screen) Healthy() bool { return !s.failed }

func (s *Screen) Flush() {
	b := s.buffer[:0]
	var last abi.Cell
	styled := false
	cursorX, cursorY := -1, -1
	for y := 0; y < s.h; y++ {
		curRow := s.cur[y]
		oldRow := s.old[y]
		for x := 0; x < s.w; {
			if curRow[x] == oldRow[x] {
				x++
				continue
			}
			start, style := x, curRow[x]
			for x < s.w && curRow[x] != oldRow[x] && curRow[x].Fg == style.Fg && curRow[x].Bg == style.Bg && curRow[x].Decor == style.Decor {
				x++
			}
			if cursorX != start || cursorY != y {
				b = append(b, "\x1b["...)
				b = strconv.AppendInt(b, int64(y+1), 10)
				b = append(b, ';')
				b = strconv.AppendInt(b, int64(start+1), 10)
				b = append(b, 'H')
			}
			reset := !styled || last.Decor != style.Decor
			if reset {
				// A decoration reset also resets colors; emit both colors afterward.
				b = append(b, "\x1b[0"...)
				for _, d := range decorCodes {
					if style.Decor&d.bit != 0 {
						b = append(b, ';', d.code)
					}
				}
				b = append(b, 'm')
			}
			if reset || last.Bg != style.Bg {
				b = appendColor(b, style.Bg, false)
			}
			if reset || last.Fg != style.Fg {
				b = appendColor(b, style.Fg, true)
			}
			last, styled = style, true
			cursorX, cursorY = x, y
			for i := start; i < x; i++ {
				ch := curRow[i].Ch
				if ch >= ' ' && ch <= '~' {
					b = append(b, byte(ch))
					continue
				}
				b = utf8.AppendRune(b, ch)
				// Non-ASCII has a terminal width that may differ from one cell;
				// reposition afterward, and at the next row to avoid autowrap.
				cursorX = -1
			}
		}
	}
	if len(b) > 0 {
		n, err := s.out.Write(b)
		s.failed = err != nil || n != len(b)
		if !s.failed {
			for y := range s.cur {
				copy(s.old[y], s.cur[y])
			}
		}
	}
	s.buffer = b[:0]
}

func appendColor(b []byte, c abi.RGB, foreground bool) []byte {
	if c == (abi.RGB{}) {
		if foreground {
			return append(b, DefaultFg...)
		}
		return append(b, DefaultBg...)
	}
	if foreground {
		b = append(b, "\x1b[38;2;"...)
	} else {
		b = append(b, "\x1b[48;2;"...)
	}
	b = strconv.AppendUint(b, uint64(c.R), 10)
	b = append(b, ';')
	b = strconv.AppendUint(b, uint64(c.G), 10)
	b = append(b, ';')
	b = strconv.AppendUint(b, uint64(c.B), 10)
	return append(b, 'm')
}
