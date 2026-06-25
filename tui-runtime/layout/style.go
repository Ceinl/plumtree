package layout

import (
	"fmt"
	"strings"
)

type TextDecoration uint8

const (
	Bold = 1 << iota
	Italic
	Underline
)

type Color struct {
	r, g, b uint8
}

type Style struct {
	foreground Color
	background Color

	TextDecoration TextDecoration
}

func (s Style) GetForeground() string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", s.foreground.r, s.foreground.g, s.foreground.b)
}

func (s Style) GetBackground() string {
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", s.background.r, s.background.g, s.background.b)
}

func (s *Style) SetForeground(r, g, b uint8) {
	s.foreground = Color{r, g, b}
}

func (s *Style) SetBackground(r, g, b uint8) {
	s.background = Color{r, g, b}
}

func (s Style) GetTextDecoration() TextDecoration {
	return s.TextDecoration
}

func (s *Style) AddTextDecoration(d TextDecoration) {
	s.TextDecoration |= d
}

func (s *Style) RemoveTextDecoration(d TextDecoration) {
	s.TextDecoration &^= d
}

func (s Style) HasTextDecoration(d TextDecoration) bool {
	return s.TextDecoration&d != 0
}

func (s Style) GetDecor() string {
	if s.TextDecoration == 0 {
		return ""
	}
	var parts []string
	if s.HasTextDecoration(Bold) {
		parts = append(parts, "1")
	}
	if s.HasTextDecoration(Italic) {
		parts = append(parts, "3")
	}
	if s.HasTextDecoration(Underline) {
		parts = append(parts, "4")
	}
	return fmt.Sprintf("\x1b[%sm", strings.Join(parts, ";"))
}
