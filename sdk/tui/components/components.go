// Package components is the Plumtree default widget toolkit, re-exported from
// the plumtree-tui runtime: Div (flex container), Text (wrapping label), and
// Button (focusable/clickable). Each implements tui.Component, so they compose
// with each other and with custom widgets.
package components

import rt "github.com/Ceinl/plumtree/tui-runtime/components"

// Widget types.
type (
	Div    = rt.Div
	Text   = rt.Text
	Button = rt.Button
	Align  = rt.Align
)

// Text horizontal alignment.
const (
	AlignLeft   = rt.AlignLeft
	AlignCenter = rt.AlignCenter
	AlignRight  = rt.AlignRight
)

// NewDiv returns an empty flex container (column, top/left aligned by default).
func NewDiv() *Div { return rt.NewDiv() }

// NewText returns a Text label displaying content; it wraps on width and
// inherits its parent's style unless one is set explicitly.
func NewText(content string) *Text { return rt.NewText(content) }

// NewButton returns a focusable, clickable button with the given label.
func NewButton(label string) *Button { return rt.NewButton(label) }
