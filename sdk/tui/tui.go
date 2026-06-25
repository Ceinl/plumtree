// Package tui exposes the Plumtree TUI runtime's layout primitives to app
// authors. It re-exports the underlying plumtree-tui runtime so apps depend
// only on the stable github.com/Ceinl/plumtree/sdk surface, not on the runtime's location.
//
// The model is: build a Component tree from state each frame; the runtime lays
// it out and diff-renders it. See github.com/Ceinl/plumtree/sdk/tui/components for widgets.
package tui

import (
	"github.com/Ceinl/plumtree/tui-runtime/layout"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
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

// Grow is a unit that expands to fill available space along the layout axis.
var Grow = layout.Unit{Type: layout.UnitGrow}

// Px returns a fixed-size unit of n cells.
func Px(n int) Unit { return layout.Unit{Type: layout.UnitPx, Value: float64(n)} }

// Percent returns a unit sized as a percentage (0–100) of the parent.
func Percent(p float64) Unit { return layout.Unit{Type: layout.UnitPercent, Value: p} }
