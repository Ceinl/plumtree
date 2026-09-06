package ui

import (
	"slices"
	"testing"
)

func TestRendererClearsPriorContentAcrossResizes(t *testing.T) {
	var renderer Renderer
	button := Button("old", "clicked").Background(RGB(12, 34, 56)).Bold()
	for _, size := range [][2]int{{12, 4}, {3, 1}, {2, 6}, {0, 0}, {20, 8}, {12, 4}} {
		renderer.Render(button, size[0], size[1])
		got := renderer.Render(Text("new"), size[0], size[1])
		want := Render(Text("new"), size[0], size[1])
		if got.Width() != want.Width() || got.Height() != want.Height() {
			t.Fatalf("wrong viewport at %dx%d", size[0], size[1])
		}
		for y := range want.cells {
			if !slices.Equal(got.cells[y], want.cells[y]) {
				t.Fatalf("stale cells or styles at %dx%d row %d", size[0], size[1], y)
			}
		}
		if len(got.hits) != 0 {
			t.Fatalf("stale button hit regions at %dx%d", size[0], size[1])
		}
	}
}

func TestRendererCloneRetainsCellsAndHitRegions(t *testing.T) {
	var renderer Renderer
	old := renderer.Render(Button("old", "clicked"), 12, 2).Clone()
	wantText := old.Text()
	renderer.Render(Button("new", "other"), 12, 2)
	if old.Text() != wantText {
		t.Fatal("snapshot cells changed after render")
	}
	event, handled := HandleFrame(old, MouseInput{Kind: MouseInputKind, X: 1, Y: 0, Button: 1, Action: 1}, nil)
	if !handled || event != "clicked" {
		t.Fatalf("snapshot hit regions changed: event=%v handled=%v", event, handled)
	}
}
