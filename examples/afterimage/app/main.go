// Afterimage is an interactive study of signals, contact, and fading memory.
package main

import (
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/timer"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type beat struct{}
type model struct {
	field        field
	w, h         int
	cursor       point
	paused, help bool
	sized        bool
}

func newModel() *model {
	return &model{field: newField(76, 15), w: 80, h: 24, cursor: point{38, 7}}
}

func (m *model) Subscriptions() app.Subscription {
	return timer.Every("afterimage", 85*time.Millisecond, beat{})
}

func (m *model) Update(event app.Event) app.Command {
	switch ev := event.(type) {
	case beat:
		if !m.paused && !m.help && m.w >= 40 && m.h >= 14 {
			m.field.step()
		}
	case app.ResizeEvent:
		m.w, m.h = max(0, ev.Width), max(0, ev.Height)
		// Bound work even on very large terminal viewports.
		w, h := min(196, max(1, m.w-4)), min(71, max(1, m.h-9))
		if !m.sized {
			m.field = newField(w, h)
			m.cursor = point{w / 2, h / 2}
			m.sized = true
		} else {
			m.field.resize(w, h)
		}
	case app.MouseEvent:
		if !m.help && m.w >= 40 && m.h >= 14 && ev.Button == app.MouseLeft && ev.Action == app.MouseDown {
			p := point{ev.X - 2, ev.Y - 4}
			if m.field.inside(p) {
				m.cursor = p
				m.field.toggle(p)
			}
		}
	case app.KeyEvent:
		switch ev.Key {
		case 'q', app.KeyCtrlC, app.KeyEscape:
			return app.Quit()
		case '?':
			m.help = !m.help
		default:
			if m.help || m.w < 40 || m.h < 14 {
				break
			}
			switch ev.Key {
			case app.KeyUp, 'k':
				m.cursor.y--
			case app.KeyDown, 'j':
				m.cursor.y++
			case app.KeyLeft, 'h':
				m.cursor.x--
			case app.KeyRight, 'l':
				m.cursor.x++
			case app.KeyEnter:
				m.field.toggle(m.cursor)
			case app.KeySpace, ' ':
				m.paused = !m.paused
			case 'n':
				m.paused = true
				m.field.step()
			case 'p':
				m.field.pulse()
			case '1', '2', '3':
				m.field.mode = int(ev.Key - '1')
			case 'c':
				clear(m.field.cells)
				m.field.sources = nil
			case 'r':
				mode := m.field.mode
				m.field = newField(m.field.w, m.field.h)
				m.field.mode = mode
			}
		}
	}
	m.cursor.x = max(0, min(m.cursor.x, m.field.w-1))
	m.cursor.y = max(0, min(m.cursor.y, m.field.h-1))
	return app.Noop()
}

func (m *model) View() ui.Node {
	return ui.Canvas(m.w, m.h, m.draw)
}

func main() { app.Run(newModel()) }
