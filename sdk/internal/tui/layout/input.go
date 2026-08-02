package layout

// MouseAction is the small component-dispatch contract used by SDK button
// automation. Coordinates are zero-based terminal cells.
type MouseAction uint8

const (
	MouseDown MouseAction = iota + 1
	MouseUp
)

type MouseEvent struct {
	X, Y   int
	Action MouseAction
}

// MouseHandler consumes a mouse event routed through a laid-out component tree.
type MouseHandler interface {
	HandleMouse(MouseEvent) bool
}
