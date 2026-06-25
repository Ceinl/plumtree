package components

import (
	"testing"

	"github.com/Ceinl/plumtree/tui-runtime/layout"
)

func TestDivLayoutColumnClamp(t *testing.T) {
	parent := NewDiv()
	parent.SetDirection(layout.Column)

	child1 := NewDiv()
	child1.SetSize(layout.Unit{Type: layout.UnitPx, Value: 10}, layout.Unit{Type: layout.UnitPx, Value: 20}) // width 10, height 20
	parent.AppendChild(child1)

	child2 := NewDiv()
	child2.SetSize(layout.Unit{Type: layout.UnitGrow}, layout.Unit{Type: layout.UnitGrow})
	parent.AppendChild(child2)

	parent.Layout(0, 0, 10, 10)

	if child1.ch != 10 {
		t.Errorf("child1 height should be clamped to innerH=10, got %d", child1.ch)
	}
	if child2.ch != 0 {
		t.Errorf("child2 (grow) height should be 0 when fixed child consumes all space, got %d", child2.ch)
	}
}

func TestDivLayoutColumnPercentClamp(t *testing.T) {
	parent := NewDiv()
	parent.SetDirection(layout.Column)

	child1 := NewDiv()
	child1.SetSize(layout.Unit{Type: layout.UnitPx, Value: 10}, layout.Unit{Type: layout.UnitPercent, Value: 150})
	parent.AppendChild(child1)

	child2 := NewDiv()
	child2.SetSize(layout.Unit{Type: layout.UnitGrow}, layout.Unit{Type: layout.UnitGrow})
	parent.AppendChild(child2)

	parent.Layout(0, 0, 10, 10)

	if child1.ch != 10 {
		t.Errorf("child1 height should be clamped to innerH=10, got %d", child1.ch)
	}
	if child2.ch != 0 {
		t.Errorf("child2 (grow) height should be 0, got %d", child2.ch)
	}
}

func TestDivLayoutRowClamp(t *testing.T) {
	parent := NewDiv()
	parent.SetDirection(layout.Row)

	child1 := NewDiv()
	child1.SetSize(layout.Unit{Type: layout.UnitPx, Value: 20}, layout.Unit{Type: layout.UnitPx, Value: 10})
	parent.AppendChild(child1)

	child2 := NewDiv()
	child2.SetSize(layout.Unit{Type: layout.UnitGrow}, layout.Unit{Type: layout.UnitGrow})
	parent.AppendChild(child2)

	parent.Layout(0, 0, 10, 10)

	if child1.cw != 10 {
		t.Errorf("child1 width should be clamped to innerW=10, got %d", child1.cw)
	}
	if child2.cw != 0 {
		t.Errorf("child2 (grow) width should be 0, got %d", child2.cw)
	}
}

func TestDivLayoutColumnGrowAllocation(t *testing.T) {
	parent := NewDiv()
	parent.SetDirection(layout.Column)

	child1 := NewDiv()
	child1.SetSize(layout.Unit{Type: layout.UnitPx, Value: 10}, layout.Unit{Type: layout.UnitPx, Value: 3})
	parent.AppendChild(child1)

	child2 := NewDiv()
	child2.SetSize(layout.Unit{Type: layout.UnitPx, Value: 10}, layout.Unit{Type: layout.UnitGrow})
	parent.AppendChild(child2)

	parent.Layout(0, 0, 10, 10)

	if child1.ch != 3 {
		t.Errorf("child1 height should be 3, got %d", child1.ch)
	}
	if child2.ch != 7 {
		t.Errorf("child2 (grow) height should be 7, got %d", child2.ch)
	}
}
