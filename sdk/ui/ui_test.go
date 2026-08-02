package ui

import "testing"

func TestDeclarativeChainsRenderThemesAndBounds(t *testing.T) {
	root := Column(
		Text("title").Role(Accent).Bold(),
		Row(Text("left"), Text("right")).Gap(1),
	).Padding(All(1)).Border(Rounded)
	frame := Render(root, 14, 5)
	if !frame.ContainsText("title") || !frame.ContainsText("left") {
		t.Fatalf("frame text = %q", frame.Text())
	}
	if got := frame.Width(); got != 14 {
		t.Fatalf("width = %d", got)
	}
	var styledTitle bool
	for y := 0; y < frame.Height(); y++ {
		for x := 0; x < frame.Width(); x++ {
			cell, _ := frame.Cell(x, y)
			if cell.Rune == 't' && cell.Style.HasForeground {
				styledTitle = true
			}
		}
	}
	if !styledTitle {
		t.Fatalf("styled title not found in frame: %q", frame.Text())
	}
	if _, ok := frame.Cell(14, 0); ok {
		t.Fatal("out-of-bounds cell was readable")
	}
}

func TestCanvasIsClippedAndStructured(t *testing.T) {
	root := Canvas(3, 2, func(surface *CanvasSurface) {
		surface.Set(-1, 0, 'x', Style{})
		surface.Set(0, 0, '\x1b', Style{})
		surface.Set(0, 0, 'A', Style{})
		surface.Set(8, 8, 'x', Style{})
		surface.SetText(1, 1, "wide", Style{})
	})
	frame := Render(root, 3, 2)
	if got := frame.Text(); got != "A  \n wi" {
		t.Fatalf("canvas text = %q", got)
	}
	for y := 0; y < frame.Height(); y++ {
		for x := 0; x < frame.Width(); x++ {
			if cell, _ := frame.Cell(x, y); cell.Rune == '\x1b' {
				t.Fatal("canvas emitted escape rune")
			}
		}
	}
}

func TestSemanticInputUsesFocusedButtonEvent(t *testing.T) {
	root := Row(Button("one", "one").Key("one"), Button("two", "two").Key("two"))
	frame := Render(root, 12, 1)
	focus := NewFocus()
	ReconcileFocus(frame.Root(), focus)
	if focus.Key() != "one" {
		t.Fatalf("initial focus = %q", focus.Key())
	}
	if event, handled := HandleFrame(frame, KeyInput{Kind: KeyInputKind, Key: KeyEnter}, focus); !handled || event != "one" {
		t.Fatalf("activation = %#v, handled=%t", event, handled)
	}
	if _, handled := HandleFrame(frame, KeyInput{Kind: KeyInputKind, Key: KeyTab}, focus); !handled || focus.Key() != "two" {
		t.Fatalf("tab focus = %q, handled=%t", focus.Key(), handled)
	}
}
