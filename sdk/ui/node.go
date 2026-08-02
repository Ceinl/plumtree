// Package ui provides the clean interactive SDK's declarative node tree.
// Nodes are ephemeral view descriptions: constructors require their essential
// data and chain modifiers only configure layout, style, semantics, or key
// identity. They do not perform I/O or mutate an app model.
package ui

import (
	"fmt"
)

// Node is a declarative UI value. The concrete node returned by each
// constructor exposes typed chain methods while Node lets composed helpers
// accept any node.
type Node interface{ nodeData() *nodeBase }

type nodeKind uint8

const (
	kindContainer nodeKind = iota + 1
	kindText
	kindButton
	kindCanvas
)

type nodeBase struct {
	kind      nodeKind
	children  []Node
	text      string
	event     any
	key       string
	style     Style
	direction Direction
	fill      bool
	gap       int
	align     Alignment
	justify   Alignment
	padding   Padding
	border    BorderStyle
	draw      func(*CanvasSurface)
}

// Container is a row or column node.
type Container struct{ nodeBase }

func (node *Container) nodeData() *nodeBase { return &node.nodeBase }

// TextNode displays bounded text.
type TextNode struct{ nodeBase }

func (node *TextNode) nodeData() *nodeBase { return &node.nodeBase }

// ButtonNode is a focusable semantic control. Activation emits Event from the
// node; it never calls a state-mutating callback.
type ButtonNode struct{ nodeBase }

func (node *ButtonNode) nodeData() *nodeBase { return &node.nodeBase }

// CanvasNode is a bounded structured drawing region.
type CanvasNode struct {
	nodeBase
	width, height int
}

func (node *CanvasNode) nodeData() *nodeBase { return &node.nodeBase }

// Direction controls child layout.
type Direction uint8

const (
	ColumnLayout Direction = iota + 1
	RowLayout
)

// Alignment controls cross-axis alignment and justification.
type Alignment uint8

const (
	Start Alignment = iota
	Center
	End
	Stretch
)

// Role is a semantic theme role.
type Role string

const (
	Default  Role = "default"
	Accent   Role = "accent"
	Muted    Role = "muted"
	Error    Role = "error"
	Success  Role = "success"
	Selected Role = "selected"
)

// Decoration is a structured text decoration.
type Decoration uint8

const (
	Bold Decoration = 1 << iota
	Dim
	Underline
)

// Color is an RGB terminal color.
type Color struct{ R, G, B uint8 }

// RGB creates a structured color value.
func RGB(r, g, b uint8) Color { return Color{R: r, G: g, B: b} }

// Style contains semantic and explicit cell presentation.
type Style struct {
	Role          Role
	Foreground    Color
	Background    Color
	Decorations   Decoration
	HasForeground bool
	HasBackground bool
}

// Padding is the number of cells inset on each side.
type Padding struct{ Top, Right, Bottom, Left int }

// All creates uniform padding.
func All(size int) Padding { return Padding{Top: size, Right: size, Bottom: size, Left: size} }

// BorderStyle is a structured border choice.
type BorderStyle uint8

const (
	NoBorder BorderStyle = iota
	Single
	Rounded
)

// Theme maps semantic roles to styles.
type Theme struct{ Roles map[Role]Style }

// DefaultTheme returns a copyable accessible default theme.
func DefaultTheme() Theme {
	return Theme{Roles: map[Role]Style{
		Default:  {},
		Accent:   {Foreground: RGB(120, 200, 255), HasForeground: true, Decorations: Bold},
		Muted:    {Foreground: RGB(140, 150, 165), HasForeground: true, Decorations: Dim},
		Error:    {Foreground: RGB(255, 120, 120), HasForeground: true, Decorations: Bold},
		Success:  {Foreground: RGB(120, 235, 150), HasForeground: true},
		Selected: {Foreground: RGB(255, 255, 255), Background: RGB(53, 54, 75), HasForeground: true, HasBackground: true},
	}}
}

// Column creates a vertically laid-out container.
func Column(children ...Node) *Container {
	return &Container{nodeBase{kind: kindContainer, children: append([]Node(nil), children...), direction: ColumnLayout, align: Stretch, justify: Start}}
}

// Row creates a horizontally laid-out container.
func Row(children ...Node) *Container {
	return &Container{nodeBase{kind: kindContainer, children: append([]Node(nil), children...), direction: RowLayout, align: Stretch, justify: Start}}
}

// Text creates a text node.
func Text(value string) *TextNode {
	return &TextNode{nodeBase{kind: kindText, text: value}}
}

// Textf creates a formatted text node.
func Textf(format string, args ...any) *TextNode { return Text(fmt.Sprintf(format, args...)) }

// Button creates a semantic focusable button.
func Button(label string, event any) *ButtonNode {
	return &ButtonNode{nodeBase{kind: kindButton, text: label, event: event}}
}

// Canvas creates a bounded structured drawing node.
func Canvas(width, height int, draw func(*CanvasSurface)) *CanvasNode {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	return &CanvasNode{nodeBase: nodeBase{kind: kindCanvas, draw: draw}, width: width, height: height}
}

func (node *nodeBase) setStyle(fn func(*Style)) { fn(&node.style) }

func (node *Container) Fill() *Container                   { node.fill = true; return node }
func (node *Container) Gap(size int) *Container            { node.gap = max(0, size); return node }
func (node *Container) Align(value Alignment) *Container   { node.align = value; return node }
func (node *Container) Justify(value Alignment) *Container { node.justify = value; return node }
func (node *Container) Padding(value Padding) *Container {
	node.padding = clampPadding(value)
	return node
}
func (node *Container) Border(value BorderStyle) *Container { node.border = value; return node }
func (node *Container) Role(value Role) *Container          { node.style.Role = value; return node }
func (node *Container) Foreground(value Color) *Container {
	node.style.Foreground = value
	node.style.HasForeground = true
	return node
}
func (node *Container) Background(value Color) *Container {
	node.style.Background = value
	node.style.HasBackground = true
	return node
}
func (node *Container) Bold() *Container            { node.style.Decorations |= Bold; return node }
func (node *Container) Key(value string) *Container { node.key = value; return node }

func (node *TextNode) Role(value Role) *TextNode { node.style.Role = value; return node }
func (node *TextNode) Foreground(value Color) *TextNode {
	node.style.Foreground = value
	node.style.HasForeground = true
	return node
}
func (node *TextNode) Background(value Color) *TextNode {
	node.style.Background = value
	node.style.HasBackground = true
	return node
}
func (node *TextNode) Bold() *TextNode            { node.style.Decorations |= Bold; return node }
func (node *TextNode) Dim() *TextNode             { node.style.Decorations |= Dim; return node }
func (node *TextNode) Underline() *TextNode       { node.style.Decorations |= Underline; return node }
func (node *TextNode) Key(value string) *TextNode { node.key = value; return node }

func (node *ButtonNode) Role(value Role) *ButtonNode { node.style.Role = value; return node }
func (node *ButtonNode) Foreground(value Color) *ButtonNode {
	node.style.Foreground = value
	node.style.HasForeground = true
	return node
}
func (node *ButtonNode) Background(value Color) *ButtonNode {
	node.style.Background = value
	node.style.HasBackground = true
	return node
}
func (node *ButtonNode) Bold() *ButtonNode            { node.style.Decorations |= Bold; return node }
func (node *ButtonNode) Key(value string) *ButtonNode { node.key = value; return node }

func (node *CanvasNode) Role(value Role) *CanvasNode { node.style.Role = value; return node }
func (node *CanvasNode) Background(value Color) *CanvasNode {
	node.style.Background = value
	node.style.HasBackground = true
	return node
}
func (node *CanvasNode) Key(value string) *CanvasNode { node.key = value; return node }

func clampPadding(value Padding) Padding {
	value.Top, value.Right, value.Bottom, value.Left = max(0, value.Top), max(0, value.Right), max(0, value.Bottom), max(0, value.Left)
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func nodeStyle(node Node) Style { return node.nodeData().style }
