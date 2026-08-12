package ui

import (
	"math"
	"testing"
)

func TestDeclarativeChainsRenderThemesAndBounds(t *testing.T) {
	root := Column(
		Text("title").Role(Accent).Bold(),
		Row(Text("left"), Text("right")).Gap(1),
	).Padding(All(1)).Border(Rounded)
	frame := Render(root, 14, 7)
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

func TestRenderIgnoresNilChildrenAndClipsOversizedCanvas(t *testing.T) {
	root := Row(nil, Canvas(10, 1, func(surface *CanvasSurface) {
		surface.Fill(0, 0, 10, 1, 'x', Style{})
	}))
	frame := Render(root, 3, 1)
	if got := frame.Text(); got != "xxx" {
		t.Fatalf("frame text = %q", got)
	}
}

func TestRenderAlignmentAndJustification(t *testing.T) {
	tests := []struct {
		name string
		root Node
		want string
	}{
		{name: "row center", root: Row(Text("x")).Align(Center).Justify(Center), want: "     \n  x  \n     "},
		{name: "row end", root: Row(Text("x")).Align(End).Justify(End), want: "     \n     \n    x"},
		{name: "column center", root: Column(Text("x")).Align(Center).Justify(Center), want: "     \n  x  \n     "},
		{name: "column end", root: Column(Text("x")).Align(End).Justify(End), want: "     \n     \n    x"},
		{name: "stretch", root: Row(Text("x")).Align(Stretch).Justify(Start), want: "x    \n     \n     "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Render(test.root, 5, 3).Text(); got != test.want {
				t.Fatalf("frame text = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRenderSanitizesControlRunes(t *testing.T) {
	frame := Render(Text("a\x1bb\u0085c"), 5, 1)
	if got := frame.Text(); got != "a�b�c" {
		t.Fatalf("frame text = %q", got)
	}
	canvas := Render(Canvas(2, 1, func(surface *CanvasSurface) {
		if surface.Set(0, 0, '\x00', Style{}) || surface.Set(1, 0, '\u009b', Style{}) {
			t.Error("canvas accepted a control rune")
		}
	}), 2, 1)
	if got := canvas.Text(); got != "  " {
		t.Fatalf("canvas text = %q", got)
	}
}

func TestHandleRequiresRenderedFrameForMouseInput(t *testing.T) {
	root := Button("click", "clicked").Key("button")
	input := MouseInput{Kind: MouseInputKind, X: 0, Y: 0, Button: 1, Action: 1}
	if event, handled := Handle(root, input, NewFocus()); handled || event != nil {
		t.Fatalf("direct mouse handling = %#v, handled=%t", event, handled)
	}
	if event, handled := HandleFrame(Render(root, 9, 1), input, NewFocus()); !handled || event != "clicked" {
		t.Fatalf("frame mouse handling = %#v, handled=%t", event, handled)
	}
}

func TestCanvasBulkOperationsClipBeforeIterating(t *testing.T) {
	frame := Render(Canvas(3, 2, func(surface *CanvasSurface) {
		if got := surface.SetText(math.MinInt, 0, "offscreen", Style{}); got != 0 {
			t.Errorf("SetText minimum-coordinate count = %d", got)
		}
		if got := surface.SetText(-2, 0, "abcd", Style{}); got != 2 {
			t.Errorf("SetText count = %d", got)
		}
		if got := surface.Fill(-1000, 1, 2000, 1000, 'x', Style{}); got != 3 {
			t.Errorf("Fill count = %d", got)
		}
	}), 3, 2)
	if got := frame.Text(); got != "cd \nxxx" {
		t.Fatalf("frame text = %q", got)
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
