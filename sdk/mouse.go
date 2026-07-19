package sdk

import "github.com/Ceinl/plumtree/tui-runtime/layout"

type mouseDispatcher struct {
	captured layout.MouseHandler
}

func (d *mouseDispatcher) Dispatch(root layout.MouseHandler, msg MouseMsg) bool {
	var action layout.MouseAction
	switch msg.Action {
	case MouseDown:
		action = layout.MouseDown
	case MouseUp:
		action = layout.MouseUp
	default:
		return false
	}
	target := root
	if msg.Action == MouseUp && d.captured != nil {
		target = d.captured
	}
	if target == nil {
		return false
	}
	consumed := target.HandleMouse(layout.MouseEvent{X: msg.X, Y: msg.Y, Action: action})
	if msg.Action == MouseDown && consumed {
		d.captured = target
	}
	if msg.Action == MouseUp {
		d.captured = nil
	}
	return consumed
}
