// Command ascii-saver renders a tiny animated night garden. It is intentionally
// interaction-light: connect over SSH, leave it running, and press q to leave.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
)

type saver struct {
	frame int
	timer sdk.CommandID
}

type scheduleFunc func(sdk.Command) (sdk.CommandID, error)

func newSaver(schedule scheduleFunc) (*saver, error) {
	timer, err := schedule(sdk.Every(90 * time.Millisecond))
	if err != nil {
		return nil, err
	}
	return &saver{timer: timer}, nil
}

func (m *saver) Update(ev sdk.Event) {
	switch e := ev.(type) {
	case sdk.TimerMsg:
		if m.timer != 0 && e.ID == m.timer {
			m.frame++
		}
	case sdk.KeyMsg:
		if e.Key == 'q' || e.Key == sdk.KeyEsc || e.Key == sdk.KeyCtrlC {
			sdk.Quit()
		}
	}
}

func (m *saver) View() tui.Component { return &nightGarden{frame: m.frame} }

type nightGarden struct {
	frame      int
	x, y, w, h int
	parent     tui.Component
}

func (*nightGarden) GetStyle() tui.Style         { return tui.Style{} }
func (*nightGarden) IsDirty() bool               { return true }
func (*nightGarden) MakeDirty()                  {}
func (*nightGarden) ClearDirty()                 {}
func (g *nightGarden) SetParent(p tui.Component) { g.parent = p }
func (g *nightGarden) Layout(x, y, w, h int)     { g.x, g.y, g.w, g.h = x, y, w, h }

var tree = []string{
	"                 .                 ",
	"              .-~~~-.              ",
	"          .-~~  * *  ~~- .         ",
	"       .-~  *  PLUMTREE  * ~-.     ",
	"      /  *   *   *   *   *   \\    ",
	"     /_*___*___*___*___*___*__\\   ",
	"              |||||               ",
	"              |||||               ",
	"          ____|||||____           ",
}

func (g *nightGarden) Render(screen *tui.Screen) {
	var sky, star, leaf, glow, trunk, hint tui.Style
	sky.SetBackground(4, 7, 18)
	sky.SetForeground(31, 43, 68)
	star.SetBackground(4, 7, 18)
	star.SetForeground(154, 220, 255)
	leaf.SetBackground(4, 7, 18)
	leaf.SetForeground(71, 214, 151)
	glow.SetBackground(4, 7, 18)
	glow.SetForeground(255, 220, 115)
	glow.AddTextDecoration(tui.Bold)
	trunk.SetBackground(4, 7, 18)
	trunk.SetForeground(181, 126, 91)
	hint.SetBackground(4, 7, 18)
	hint.SetForeground(87, 103, 135)

	fill(screen, g.x, g.y, g.w, g.h, ' ', sky)
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			if starAt(x, y, g.frame) {
				glyphs := ".+*"
				r := rune(glyphs[(x+y+g.frame/3)%len(glyphs)])
				put(screen, g.x+x, g.y+y, r, star)
			}
		}
	}

	artWidth := len([]rune(tree[0]))
	left := g.x + (g.w-artWidth)/2
	top := g.y + (g.h-len(tree))/2
	if top < g.y {
		top = g.y
	}
	for row, line := range tree {
		for col, r := range []rune(line) {
			if r == ' ' {
				continue
			}
			style := leaf
			switch {
			case r == '|' || r == '_':
				style = trunk
			case r == '*' || strings.ContainsRune("PLUMTREE", r):
				style = glow
			}
			// A one-cell shimmer makes the canopy feel alive without raw ANSI.
			x := left + col
			if row < 6 && (g.frame/5)%2 == 1 && row%2 == 0 {
				x++
			}
			put(screen, x, top+row, r, style)
		}
	}
	footer := "ssh garden // q to disconnect"
	drawText(screen, g.x+(g.w-len([]rune(footer)))/2, g.y+g.h-2, footer, hint)
}

func starAt(x, y, frame int) bool {
	// Stable integer noise, with a slow phase shift for twinkling.
	v := uint32(x*73856093) ^ uint32(y*19349663) ^ uint32((frame/4)*83492791)
	return v%97 == 0
}

func fill(s *tui.Screen, x, y, w, h int, r rune, style tui.Style) {
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			put(s, x+col, y+row, r, style)
		}
	}
}

func drawText(s *tui.Screen, x, y int, text string, style tui.Style) {
	for i, r := range []rune(text) {
		put(s, x+i, y, r, style)
	}
}

func put(s *tui.Screen, x, y int, r rune, style tui.Style) {
	s.Set(x, y, r, style.GetForeground(), style.GetBackground(), style.GetDecor())
}

func main() {
	m, err := newSaver(sdk.Schedule)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ascii-saver:", err)
		return
	}
	sdk.RunTUI(m, sdk.Meta{Name: "ascii-saver", Type: "tui"})
}
