package components

import (
	"testing"

	"github.com/Ceinl/plumtree/tui-runtime/layout"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
)

func TestDivRoutesNestedButtonClick(t *testing.T) {
	root := NewDiv()
	nested := NewDiv()
	nested.SetSize(layout.Unit{Type: layout.UnitGrow}, layout.Unit{Type: layout.UnitGrow})
	button := NewButton("click")
	clicked := 0
	button.OnClick = func() { clicked++ }
	nested.AppendChild(button)
	root.AppendChild(nested)
	root.Layout(0, 0, 10, 4)
	if !root.HandleMouse(layout.MouseEvent{X: 2, Y: 1, Action: layout.MouseDown}) ||
		!root.HandleMouse(layout.MouseEvent{X: 2, Y: 1, Action: layout.MouseUp}) {
		t.Fatal("nested click was not consumed")
	}
	if clicked != 1 {
		t.Fatalf("clicks = %d", clicked)
	}
}

type mouseProbe struct {
	name string
	log  *[]string
}

func (p *mouseProbe) GetStyle() layout.Style     { return layout.Style{} }
func (p *mouseProbe) IsDirty() bool              { return false }
func (p *mouseProbe) MakeDirty()                 {}
func (p *mouseProbe) ClearDirty()                {}
func (p *mouseProbe) Layout(int, int, int, int)  {}
func (p *mouseProbe) Render(*screen.Screen)      {}
func (p *mouseProbe) SetParent(layout.Component) {}
func (p *mouseProbe) HandleMouse(layout.MouseEvent) bool {
	*p.log = append(*p.log, p.name)
	return true
}

func TestDivRoutesTopmostFirst(t *testing.T) {
	var log []string
	root := NewDiv()
	root.AppendChild(&mouseProbe{name: "bottom", log: &log})
	root.AppendChild(&mouseProbe{name: "top", log: &log})
	root.HandleMouse(layout.MouseEvent{Action: layout.MouseDown})
	if len(log) != 1 || log[0] != "top" {
		t.Fatalf("dispatch order = %v", log)
	}
}
