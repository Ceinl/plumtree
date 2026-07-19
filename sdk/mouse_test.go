package sdk

import (
	"testing"

	"github.com/Ceinl/plumtree/sdk/tui/components"
)

func TestAutomaticButtonMouseDispatch(t *testing.T) {
	root := components.NewDiv()
	button := components.NewButton("click")
	clicked := 0
	button.OnClick = func() { clicked++ }
	root.AppendChild(button)
	root.Layout(0, 0, 12, 3)
	var dispatch mouseDispatcher
	if !dispatch.Dispatch(root, MouseMsg{X: 4, Y: 1, Button: MouseButtonLeft, Action: MouseDown}) {
		t.Fatal("button did not consume down")
	}
	// Simulate an immediate-mode redraw that produced a different tree. Mouse-up
	// must still return to the exact tree that captured mouse-down.
	replacement := components.NewDiv()
	replacement.Layout(0, 0, 12, 3)
	if !dispatch.Dispatch(replacement, MouseMsg{X: 4, Y: 1, Button: MouseButtonLeft, Action: MouseUp}) {
		t.Fatal("button did not consume click")
	}
	if clicked != 1 {
		t.Fatalf("clicks = %d", clicked)
	}
}
