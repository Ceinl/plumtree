package main

import (
	"slices"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/plumtest"
)

func TestSignalsMixAndRest(t *testing.T) {
	for mode := range rules {
		f := field{w: 5, h: 5, cells: make([]cell, 25), next: make([]cell, 25), mode: mode}
		f.cells[11] = cell{life: 1, trace: 28, ink: 1}
		wantInk := uint8(1)
		if mode != 0 {
			f.cells[13] = cell{life: 1, trace: 28, ink: 2}
			wantInk = 3
		}
		f.step()
		if c := f.cells[12]; c.life != 1 || c.ink != wantInk {
			t.Fatalf("mode %d: centre did not fire with mixed ink: %+v", mode, c)
		}
		f.step()
		if c := f.cells[12]; c.life != rules[mode].rest || c.trace != 27 {
			t.Fatalf("mode %d: fired cell did not rest and fade: %+v", mode, c)
		}
	}
}

func TestTraceDoesNotFireAndEdgesDoNotWrap(t *testing.T) {
	f := field{w: 5, h: 5, cells: make([]cell, 25), next: make([]cell, 25)}
	f.cells[0] = cell{life: 1, trace: 28, ink: 1}
	f.cells[18] = cell{trace: 20, ink: 2}
	f.step()
	for _, i := range []int{4, 9, 14, 17, 19, 20, 21, 22, 23, 24} {
		if f.cells[i].life != 0 {
			t.Fatalf("unexpected signal at index %d", i)
		}
	}
}

func TestControlsAndVirtualClock(t *testing.T) {
	m := newModel()
	r := plumtest.Start(t, m)
	r.Resize(80, 24)
	r.ExpectText("A F T E R I M A G E")
	r.Advance(85 * time.Millisecond)
	if m.field.tick != 1 {
		t.Fatal("timer did not advance field")
	}
	r.Key(' ')
	before := slices.Clone(m.field.cells)
	r.Advance(time.Second)
	if !slices.Equal(before, m.field.cells) {
		t.Fatal("paused field changed")
	}
	r.Key('n')
	if m.field.tick != 2 {
		t.Fatal("single step did not advance exactly once")
	}
	r.Key('c')
	r.Mouse(12, 9, app.MouseLeft, app.MouseDown)
	if len(m.field.sources) != 1 || m.field.sources[0].point != (point{10, 5}) {
		t.Fatal("mouse did not place source in field coordinates")
	}
	r.Key(app.KeyEnter)
	if len(m.field.sources) != 0 {
		t.Fatal("Enter did not remove source")
	}
	r.Key('3')
	r.ExpectText("BLOOM / PAUSED")
	r.Key('?')
	r.ExpectText("A NOTE FROM THE AGENT")
	r.Key(' ')
	if !m.paused {
		t.Fatal("help did not block field controls")
	}
	r.Key('?')
	r.Key('q')
	r.ExpectQuit()
}

func TestResizePreservesFieldAndBoundsWork(t *testing.T) {
	m := newModel()
	r := plumtest.Start(t, m)
	r.Resize(80, 24)
	m.field.toggle(point{2, 2})
	before := m.field.cells[2*m.field.w+2]
	r.Resize(45, 16)
	if got := m.field.cells[2*m.field.w+2]; got != before {
		t.Fatal("resize lost overlapping cell")
	}
	r.Resize(10, 4)
	r.ExpectText("AFTERIMAGE")
	r.Advance(time.Second)
	if m.field.tick != 0 {
		t.Fatal("small viewport should suspend simulation")
	}
	r.Resize(220, 90)
	if m.field.w > 196 || m.field.h > 71 {
		t.Fatal("field exceeded work limit")
	}
}

func TestSmallInitialViewportHasSources(t *testing.T) {
	m := newModel()
	r := plumtest.Start(t, m, plumtest.Viewport(40, 14))
	r.Resize(40, 14)
	if len(m.field.sources) != 3 {
		t.Fatal("initial sources lost on small viewport")
	}
	r.Key('?')
	r.ExpectText("c clear  r reset")
	r.ExpectText("? return   q quit")
}

func TestSourcesPulseAndStayBounded(t *testing.T) {
	f := newField(40, 20)
	clear(f.cells)
	for range 24 {
		f.step()
	}
	for _, s := range f.sources {
		if f.cells[s.y*f.w+s.x].life != 1 {
			t.Fatal("source did not send periodic pulse")
		}
	}
	for x := range 40 {
		f.toggle(point{x, 0})
	}
	if len(f.sources) != 24 {
		t.Fatalf("source count = %d, want 24", len(f.sources))
	}
}
