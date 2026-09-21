package main

import (
	"fmt"

	"github.com/Ceinl/plumtree/sdk/ui"
)

var background = ui.RGB(9, 13, 23)

func style(c ui.Color) ui.Style {
	return ui.Style{Foreground: c, Background: background, HasForeground: true, HasBackground: true}
}

// Mixed signals combine their pigments. Old traces dim without influencing
// the next generation, so the screen shows both activity and its history.
func pigment(ink uint8, trace uint8) ui.Color {
	colors := [...]ui.Color{ui.RGB(75, 225, 207), ui.RGB(255, 167, 104), ui.RGB(180, 144, 255)}
	r, g, b, count := 0, 0, 0, 0
	for i, c := range colors {
		if ink&(1<<i) != 0 {
			r += int(c.R)
			g += int(c.G)
			b += int(c.B)
			count++
		}
	}
	if count == 0 {
		return background
	}
	factor := int(trace) * int(trace)
	return ui.RGB(uint8(r/count*factor/784), uint8(g/count*factor/784), uint8(b/count*factor/784))
}

func (m *model) draw(s *ui.CanvasSurface) {
	muted, bright := style(ui.RGB(113, 130, 151)), style(ui.RGB(221, 230, 242))
	s.Fill(0, 0, s.Width(), s.Height(), ' ', style(background))
	if m.w < 40 || m.h < 14 {
		s.SetText(0, 0, "AFTERIMAGE", bright)
		s.SetText(0, 2, "Please resize to at least 40 x 14.", muted)
		s.SetText(0, 3, "q quit", muted)
		return
	}
	s.SetText(2, 1, "A F T E R I M A G E", style(ui.RGB(75, 225, 207)))
	subtitle := "a small machine for unfinished thoughts"
	if m.w < 44 {
		subtitle = "a machine for unfinished thoughts"
	}
	s.SetText(2, 2, subtitle, muted)
	if m.help {
		lines := []string{
			"A NOTE FROM THE AGENT", "",
			"I chose a field of signals as my self-portrait.",
			"Each follows a small rule. Their contact makes the form.",
			"This is an artwork, not a model of my internal state.", "",
			"+ sources send signals every 24 steps.",
			"Bright cells fire. Faint cells remember, then fade.",
			"Colours mix where signals meet. Edges absorb them.", "",
			"Click / arrows + Enter: source",
			"1 ripple  2 branch  3 bloom",
			"Space pause  n step  p pulse",
			"c clear  r reset",
			"? return   q quit",
		}
		// On short screens, keep the controls visible above the artist's note.
		if m.h < 23 || m.w < 60 {
			lines = append([]string{"CONTROLS", ""}, lines[10:]...)
		}
		for i, line := range lines {
			s.SetText(2, 4+i, line, bright)
		}
		return
	}
	active := 0
	for y := 0; y < m.field.h; y++ {
		for x := 0; x < m.field.w; x++ {
			c := m.field.cells[y*m.field.w+x]
			mark := ' '
			if c.life == 1 {
				mark = '*'
				active++
			} else if c.trace > 21 {
				mark = 'o'
			} else if c.trace > 14 {
				mark = ':'
			} else if c.trace > 5 {
				mark = '.'
			}
			if mark != ' ' {
				s.Set(x+2, y+4, mark, style(pigment(c.ink, c.trace)))
			} else if x%8 == 0 && y%4 == 0 {
				s.Set(x+2, y+4, '.', style(ui.RGB(28, 38, 53)))
			}
		}
	}
	for _, source := range m.field.sources {
		s.Set(source.x+2, source.y+4, '+', style(pigment(source.ink, 28)))
	}
	c := m.field.cells[m.cursor.y*m.field.w+m.cursor.x]
	cursorStyle := style(background)
	cursorStyle.Background = ui.RGB(221, 230, 242)
	mark := 'X'
	for _, source := range m.field.sources {
		if source.point == m.cursor {
			mark = '+'
		}
	}
	if c.life == 1 && mark != '+' {
		mark = '*'
	}
	s.Set(m.cursor.x+2, m.cursor.y+4, mark, cursorStyle)
	y := m.h - 4
	state := "LIVE"
	if m.paused {
		state = "PAUSED"
	}
	status := fmt.Sprintf("%s / %s   %06d   %d lit   %d/24 sources", rules[m.field.mode].name, state, m.field.tick, active, len(m.field.sources))
	if m.w < 76 {
		status = fmt.Sprintf("%s / %s  %d/24 sources", rules[m.field.mode].name, state, len(m.field.sources))
	}
	s.SetText(2, y, status, bright)
	if m.w >= 76 {
		s.SetText(2, y+1, rules[m.field.mode].note, muted)
		s.SetText(2, y+2, "click plant  1/2/3 rule  space pause  n step  r reset  ? help  q quit", muted)
	} else {
		s.SetText(2, y+1, "arrows + enter plant | 1/2/3 rule", muted)
		s.SetText(2, y+2, "space pause | ? help | q quit", muted)
	}
}
