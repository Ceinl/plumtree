package sdk

// Event is an input delivered to Model.Update. Type-switch on the concrete
// types (KeyMsg, ResizeMsg, TimerMsg) to handle it.
type Event interface{ isEvent() }

// Key identifies a key press. Printable keys equal their rune (so a switch can
// compare Key to 'q'); named keys are distinct negative values.
type Key rune

// Named (non-printable) keys. Negative so they never collide with a rune.
const (
	KeyUp Key = -(iota + 1)
	KeyDown
	KeyLeft
	KeyRight
	KeyEnter
	KeyEsc
	KeyTab
	KeyBackspace
	KeyDelete
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyCtrlC
)

// KeyMsg is a key-press event. Key holds the key (a rune for printable keys or
// a named Key constant); the bools report active modifiers.
type KeyMsg struct {
	Key   Key
	Shift bool
	Ctrl  bool
	Alt   bool
	Cmd   bool
}

func (KeyMsg) isEvent() {}

// Rune reports the printable rune for this key, or 0 for a named key.
func (k KeyMsg) Rune() rune {
	if k.Key >= 0 {
		return rune(k.Key)
	}
	return 0
}

// ResizeMsg reports a new terminal size in cells.
type ResizeMsg struct{ W, H int }

func (ResizeMsg) isEvent() {}

// MessageMsg is a pub/sub message published to a topic this app subscribed to
// (see Subscribe). It is delivered to Model.Update of every session subscribed
// to Topic, including the publisher's own session, so live shared state can
// update without polling.
type MessageMsg struct {
	Topic string
	Data  []byte
}

func (MessageMsg) isEvent() {}

type MouseButton uint8

const (
	MouseButtonNone MouseButton = iota
	MouseButtonLeft
)

type MouseAction uint8

const (
	MouseDown MouseAction = iota + 1
	MouseUp
	MouseDrag
	MouseWheelUp
	MouseWheelDown
)

// MouseMsg reports a mouse action at zero-based terminal cell coordinates.
// SDK buttons automatically receive down/up events; Model.Update still gets the
// message so custom components can inspect it.
type MouseMsg struct {
	X, Y   int
	Button MouseButton
	Action MouseAction
}

func (MouseMsg) isEvent() {}

// TimerMsg reports completion of a command created by After or Every. ID is
// the value returned by Schedule and lets a model distinguish multiple timers.
// Recurring timers deliver the same ID until canceled.
type TimerMsg struct {
	ID CommandID
}

func (TimerMsg) isEvent() {}
