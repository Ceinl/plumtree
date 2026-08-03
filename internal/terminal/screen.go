package terminal

import (
	"fmt"
	"io"
	"os"
)

const (
	DefaultBg = "\x1b[48;2;25;23;29m"
	DefaultFg = "\x1b[38;2;200;200;200m"
	MinWidth  = 1
	MaxWidth  = 500
	MinHeight = 1
	MaxHeight = 300
	MaxCells  = 150_000
)

type cell struct {
	ch            rune
	fg, bg, decor string
}
type Screen struct {
	w, h     int
	old, cur [][]cell
	out      io.Writer
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
	s.old = make([][]cell, h)
	s.cur = make([][]cell, h)
	for y := range s.cur {
		s.old[y] = make([]cell, w)
		s.cur[y] = make([]cell, w)
		for x := range s.cur[y] {
			s.cur[y][x] = cell{ch: ' ', fg: DefaultFg, bg: DefaultBg}
		}
	}
}
func (s *Screen) Set(x, y int, ch rune, fg, bg, decor string) {
	if x < 0 || x >= s.w || y < 0 || y >= s.h {
		return
	}
	if fg == "" {
		fg = DefaultFg
	}
	if bg == "" {
		bg = DefaultBg
	}
	s.cur[y][x] = cell{ch: ch, fg: fg, bg: bg, decor: decor}
}
func (s *Screen) Flush() {
	for y := 0; y < s.h; y++ {
		for x := 0; x < s.w; {
			if s.cur[y][x] == s.old[y][x] {
				x++
				continue
			}
			start := x
			style := s.cur[y][x]
			for x < s.w && s.cur[y][x] != s.old[y][x] && s.cur[y][x].fg == style.fg && s.cur[y][x].bg == style.bg && s.cur[y][x].decor == style.decor {
				x++
			}
			fmt.Fprintf(s.out, "\x1b[%d;%dH%s%s%s", y+1, start+1, style.decor, style.bg, style.fg)
			for i := start; i < x; i++ {
				fmt.Fprint(s.out, string(s.cur[y][i].ch))
				s.old[y][i] = s.cur[y][i]
			}
		}
	}
}
