package components

import (
	"github.com/Ceinl/plumtree/tui-runtime/layout"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
)

// Button is a focusable, clickable widget that fills its box and draws a
// centered label. It carries three styles — normal, focused and pressed — and
// renders whichever matches its current state.
//
// Button is input-agnostic: the host translates terminal events into calls on
// HandleMouseDown/HandleMouseUp (for the mouse) or Activate (e.g. Enter on a
// focused button). OnClick fires on a completed click or an Activate.
type Button struct {
	isDirty bool
	label   string
	parent  layout.Component
	x, y    int
	w, h    int

	normal  layout.Style
	focus   layout.Style
	pressed layout.Style

	isFocused bool
	isPressed bool

	// OnClick is invoked when the button is activated by mouse or keyboard.
	OnClick func()
}

// NewButton returns a button with the given label and a default style set.
func NewButton(label string) *Button {
	b := &Button{label: label, isDirty: true}
	b.normal = solid(48, 45, 61, 232, 229, 241)
	b.focus = solid(72, 68, 96, 238, 234, 248)
	b.pressed = solid(96, 90, 128, 255, 255, 255)
	return b
}

func solid(br, bgc, bb, fr, fg, fb uint8) layout.Style {
	var s layout.Style
	s.SetBackground(br, bgc, bb)
	s.SetForeground(fr, fg, fb)
	return s
}

func (b *Button) IsDirty() bool { return b.isDirty }
func (b *Button) MakeDirty()    { b.isDirty = true }
func (b *Button) ClearDirty()   { b.isDirty = false }

func (b *Button) Layout(x, y, w, h int) {
	b.x, b.y, b.w, b.h = x, y, w, h
}

func (b *Button) SetParent(p layout.Component) { b.parent = p }

// GetStyle returns the style for the button's current state.
func (b *Button) GetStyle() layout.Style {
	switch {
	case b.isPressed:
		return b.pressed
	case b.isFocused:
		return b.focus
	default:
		return b.normal
	}
}

// SetStyles overrides the normal, focused and pressed styles.
func (b *Button) SetStyles(normal, focus, pressed layout.Style) {
	b.normal, b.focus, b.pressed = normal, focus, pressed
	b.isDirty = true
}

// SetLabel replaces the button text.
func (b *Button) SetLabel(label string) {
	if b.label != label {
		b.label = label
		b.isDirty = true
	}
}

// Label returns the current button text.
func (b *Button) Label() string { return b.label }

// SetFocused sets keyboard focus on the button.
func (b *Button) SetFocused(f bool) {
	if b.isFocused != f {
		b.isFocused = f
		b.isDirty = true
	}
}

// Focused reports whether the button currently has focus.
func (b *Button) Focused() bool { return b.isFocused }

// HitTest reports whether (x, y) falls inside the button's box.
func (b *Button) HitTest(x, y int) bool {
	return x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h
}

// Activate fires OnClick. Use it for keyboard activation of a focused button.
func (b *Button) Activate() {
	if b.OnClick != nil {
		b.OnClick()
	}
}

// HandleMouseDown registers a press if (x, y) is inside the button. It focuses
// the button and returns true when the event is consumed.
func (b *Button) HandleMouseDown(x, y int) bool {
	if !b.HitTest(x, y) {
		return false
	}
	b.SetFocused(true)
	if !b.isPressed {
		b.isPressed = true
		b.isDirty = true
	}
	return true
}

// HandleMouseUp completes a click: if the button was pressed and the release
// lands inside it, OnClick fires. It returns true when a press was released
// (regardless of where), so callers can stop hit-testing other widgets.
func (b *Button) HandleMouseUp(x, y int) bool {
	if !b.isPressed {
		return false
	}
	b.isPressed = false
	b.isDirty = true
	if b.HitTest(x, y) {
		b.Activate()
	}
	return true
}

func (b *Button) Render(s *screen.Screen) {
	if b.w <= 0 || b.h <= 0 {
		return
	}
	st := b.GetStyle()
	bg, fg, decor := st.GetBackground(), st.GetForeground(), st.GetDecor()

	for y := b.y; y < b.y+b.h; y++ {
		for x := b.x; x < b.x+b.w; x++ {
			s.Set(x, y, ' ', fg, bg, decor)
		}
	}

	runes := []rune(b.label)
	if len(runes) > b.w {
		runes = runes[:b.w]
	}
	tx := b.x + (b.w-len(runes))/2
	ty := b.y + b.h/2
	for i, r := range runes {
		s.Set(tx+i, ty, r, fg, bg, decor)
	}
}
