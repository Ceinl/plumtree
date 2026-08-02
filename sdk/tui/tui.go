// Package tui exposes the Plumtree SDK's layout primitives to app authors.
// The implementation remains private so apps depend only on this stable public
// surface, not on the runtime's location.
//
// The model is: build a Component tree from state each frame; the runtime lays
// it out and diff-renders it. See github.com/Ceinl/plumtree/sdk/tui/components for widgets.
package tui

import (
	"io"

	"github.com/Ceinl/plumtree/sdk/internal/tui/layout"
	"github.com/Ceinl/plumtree/sdk/internal/tui/screen"
)

// Component is anything the runtime can lay out and render. Every widget
// implements it; custom widgets implement it too.
type Component = layout.Component

// Layout primitive types.
type (
	Unit           = layout.Unit
	UnitType       = layout.UnitType
	Direction      = layout.Direction
	JustifyContent = layout.JustifyContent
	AlignItems     = layout.AlignItems
	Padding        = layout.Padding
	Style          = layout.Style
	TextDecoration = layout.TextDecoration
	MouseAction    = layout.MouseAction
	MouseEvent     = layout.MouseEvent
	MouseHandler   = layout.MouseHandler
)

// Screen is the cell buffer a Component renders into (needed only when
// implementing a custom Component).
type Screen = screen.Screen

// Sizing unit kinds.
const (
	UnitPx      = layout.UnitPx
	UnitPercent = layout.UnitPercent
	UnitGrow    = layout.UnitGrow
)

// Screen bounds used by host-side terminal integrations.
const (
	DefaultBg = screen.DefaultBg
	DefaultFg = screen.DefaultFg
	MinWidth  = screen.MinWidth
	MaxWidth  = screen.MaxWidth
	MinHeight = screen.MinHeight
	MaxHeight = screen.MaxHeight
	MaxCells  = screen.MaxCells
)

// Layout direction.
const (
	Column = layout.Column
	Row    = layout.Row
)

// Main-axis distribution.
const (
	JCenter = layout.JCenter
	JLeft   = layout.JLeft
	JRight  = layout.JRight
)

// Cross-axis alignment.
const (
	ACenter = layout.ACenter
	ATop    = layout.ATop
	ABottom = layout.ABottom
	ALeft   = layout.ALeft
	ARight  = layout.ARight
)

// Text decorations.
const (
	Bold      = layout.Bold
	Italic    = layout.Italic
	Underline = layout.Underline
)

// Mouse actions.
const (
	MouseDown = layout.MouseDown
	MouseUp   = layout.MouseUp
)

// NewScreen returns a cell buffer for rendering a component tree.
func NewScreen(w, h int) *Screen { return screen.NewScreen(w, h) }

// NewScreenWithOutput returns a cell buffer that flushes to out instead of
// stdout. Host integrations use it for SSH and other network-backed sessions.
func NewScreenWithOutput(w, h int, out io.Writer) *Screen {
	return screen.NewScreenWithOutput(w, h, out)
}

// Grow is a unit that expands to fill available space along the layout axis.
var Grow = layout.Unit{Type: layout.UnitGrow}

// Px returns a fixed-size unit of n cells.
func Px(n int) Unit { return layout.Unit{Type: layout.UnitPx, Value: float64(n)} }

// Percent returns a unit sized as a percentage (0–100) of the parent.
func Percent(p float64) Unit { return layout.Unit{Type: layout.UnitPercent, Value: p} }
