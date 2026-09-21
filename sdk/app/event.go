package app

// Key is a named or printable keyboard key. Printable keys use their rune
// value; named keys are negative so they cannot collide with Unicode input.
type Key rune

const (
	KeyUp Key = -(iota + 1)
	KeyDown
	KeyLeft
	KeyRight
	KeyEnter
	KeyEscape
	KeyTab
	KeyBackspace
	KeyDelete
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeySpace
	KeyCtrlC
)

// KeyEvent is an unhandled keyboard input event.
type KeyEvent struct {
	Key   Key
	Shift bool
	Ctrl  bool
	Alt   bool
	Cmd   bool
}

// Rune returns a printable rune or zero for a named key.
func (event KeyEvent) Rune() rune {
	if event.Key >= 0 {
		return rune(event.Key)
	}
	return 0
}

// MouseButton identifies a pointer button.
type MouseButton uint8

const (
	MouseNone MouseButton = iota
	MouseLeft
	MouseRight
	MouseMiddle
)

// MouseAction identifies a pointer action.
type MouseAction uint8

const (
	MouseDown MouseAction = iota + 1
	MouseUp
	MouseDrag
	MouseWheelUp
	MouseWheelDown
)

// MouseEvent reports zero-based terminal-cell coordinates.
type MouseEvent struct {
	X, Y   int
	Button MouseButton
	Action MouseAction
}

// PasteEvent is a bounded bracketed-paste value.
type PasteEvent struct{ Text string }

// ResizeEvent reports a terminal size in cells.
type ResizeEvent struct{ Width, Height int }

// MessageEvent is a clean ABI pub/sub notification. Capability packages may
// consume it through a subscription filter without coupling app to bus.
type MessageEvent struct {
	Topic string
	Data  []byte
}
