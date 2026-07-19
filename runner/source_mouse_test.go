package runner

import (
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
)

func TestMapMouseInput(t *testing.T) {
	cases := []struct {
		type_  keyboard.EventType
		action abi.MouseAction
		button abi.MouseButton
	}{
		{keyboard.KeyMouseLeftDown, abi.MouseDown, abi.MouseButtonLeft},
		{keyboard.KeyMouseLeftUp, abi.MouseUp, abi.MouseButtonLeft},
		{keyboard.KeyMouseLeftDrag, abi.MouseDrag, abi.MouseButtonLeft},
		{keyboard.KeyMouseWheelUp, abi.MouseWheelUp, abi.MouseButtonNone},
		{keyboard.KeyMouseWheelDown, abi.MouseWheelDown, abi.MouseButtonNone},
	}
	for _, tc := range cases {
		got, ok := mapInput(keyboard.Event{Type: tc.type_, Mouse: true, MouseX: 9, MouseY: 4})
		if !ok || got.Kind != abi.KindMouse || got.MouseX != 9 || got.MouseY != 4 || got.Action != tc.action || got.Button != tc.button {
			t.Errorf("mapInput(%v) = %+v, %v", tc.type_, got, ok)
		}
	}
}
