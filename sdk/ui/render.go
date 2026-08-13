package ui

import (
	"strings"
)

// Cell is one structured terminal cell. It contains no raw terminal control
// sequence; the host owns serialization to terminal output.
type Cell struct {
	Rune  rune
	Style Style
}

// Frame is a bounded structured render result.
type Frame struct {
	width, height int
	root          Node
	cells         [][]Cell
	hits          []hitRegion
}

// Render lays out and draws node into a bounded frame using DefaultTheme.
func Render(node Node, width, height int) Frame {
	return RenderWithTheme(node, width, height, DefaultTheme())
}

// RenderWithTheme renders with a caller-provided semantic theme.
func RenderWithTheme(node Node, width, height int, theme Theme) Frame {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	frame := Frame{width: width, height: height, root: node, cells: make([][]Cell, height)}
	for y := range frame.cells {
		frame.cells[y] = make([]Cell, width)
		for x := range frame.cells[y] {
			frame.cells[y][x].Rune = ' '
		}
	}
	if node != nil && width > 0 && height > 0 {
		renderNode(&frame, node, 0, 0, width, height, theme)
	}
	return frame
}

// Width returns the frame width.
func (frame Frame) Width() int { return frame.width }

// Height returns the frame height.
func (frame Frame) Height() int { return frame.height }

// Root returns the ephemeral root node used to produce the frame.
func (frame Frame) Root() Node { return frame.root }

// Clone returns a detached frame copy.
func (frame Frame) Clone() Frame {
	clone := Frame{width: frame.width, height: frame.height, root: frame.root, cells: make([][]Cell, len(frame.cells)), hits: append([]hitRegion(nil), frame.hits...)}
	for y := range frame.cells {
		clone.cells[y] = append([]Cell(nil), frame.cells[y]...)
	}
	return clone
}

// Cell returns a cell and whether its coordinates are in bounds.
func (frame Frame) Cell(x, y int) (Cell, bool) {
	if y < 0 || y >= len(frame.cells) || x < 0 || x >= frame.width {
		return Cell{}, false
	}
	return frame.cells[y][x], true
}

// Text returns the frame as newline-separated text, retaining its rectangular
// dimensions so frame tests are deterministic.
func (frame Frame) Text() string {
	lines := make([]string, len(frame.cells))
	for y, row := range frame.cells {
		var builder strings.Builder
		for _, cell := range row {
			if cell.Rune == 0 {
				builder.WriteRune(' ')
			} else {
				builder.WriteRune(cell.Rune)
			}
		}
		lines[y] = builder.String()
	}
	return strings.Join(lines, "\n")
}

// ContainsText reports whether the visible frame contains value.
func (frame Frame) ContainsText(value string) bool { return strings.Contains(frame.Text(), value) }

type hitRegion struct {
	x, y, width, height int
	node                Node
}

func renderNode(frame *Frame, node Node, x, y, width, height int, theme Theme) {
	if width <= 0 || height <= 0 || node == nil {
		return
	}
	base := node.nodeData()
	style := resolveStyle(base.style, theme)
	if base.kind == kindButton {
		frame.hits = append(frame.hits, hitRegion{x: x, y: y, width: width, height: height, node: node})
	}
	if base.border != NoBorder {
		drawBorder(frame, x, y, width, height, base.border, style)
		x, y, width, height = x+1, y+1, width-2, height-2
	}
	if width <= 0 || height <= 0 {
		return
	}
	padding := base.padding
	x += padding.Left
	y += padding.Top
	width -= padding.Left + padding.Right
	height -= padding.Top + padding.Bottom
	if width <= 0 || height <= 0 {
		return
	}

	switch base.kind {
	case kindText, kindButton:
		text := base.text
		if base.kind == kindButton {
			text = "[ " + text + " ]"
		}
		drawText(frame, x, y, width, height, text, style)
	case kindCanvas:
		drawCanvas(frame, node.(*CanvasNode), x, y, width, height, style)
	case kindContainer:
		renderChildren(frame, base, x, y, width, height, theme)
	}
}

func renderChildren(frame *Frame, base *nodeBase, x, y, width, height int, theme Theme) {
	children := make([]Node, 0, len(base.children))
	for _, child := range base.children {
		if child != nil {
			children = append(children, child)
		}
	}
	if len(children) == 0 {
		return
	}
	mainSize := width
	if base.direction == ColumnLayout {
		mainSize = height
	}
	gaps := max(0, len(children)-1) * base.gap
	remaining := max(0, mainSize-gaps)
	intrinsic := make([]int, len(children))
	fillCount := 0
	for index, child := range children {
		intrinsic[index] = intrinsicMain(child, base.direction, width, height)
		if child.nodeData().fill {
			fillCount++
		} else {
			remaining -= intrinsic[index]
		}
	}
	fillSize := 0
	if fillCount > 0 {
		fillSize = max(0, remaining) / fillCount
	}
	contentSize := gaps
	for index, child := range children {
		contentSize += intrinsic[index]
		if child.nodeData().fill {
			contentSize += fillSize - intrinsic[index]
		}
	}
	position := 0
	free := max(0, mainSize-contentSize)
	switch base.justify {
	case Center:
		position = free / 2
	case End:
		position = free
	}
	for index, child := range children {
		size := intrinsic[index]
		if child.nodeData().fill {
			size = fillSize
		}
		if index == len(children)-1 && child.nodeData().fill {
			size = max(0, mainSize-position)
		}
		size = min(size, max(0, mainSize-position))
		crossSize := intrinsicCross(child, base.direction, width, height)
		crossLimit := height
		if base.direction == ColumnLayout {
			crossLimit = width
		}
		crossSize = min(crossSize, crossLimit)
		crossPosition := 0
		switch base.align {
		case Stretch:
			crossSize = crossLimit
		case Center:
			crossPosition = max(0, crossLimit-crossSize) / 2
		case End:
			crossPosition = max(0, crossLimit-crossSize)
		}
		if base.direction == ColumnLayout {
			renderNode(frame, child, x+crossPosition, y+position, crossSize, size, theme)
		} else {
			renderNode(frame, child, x+position, y+crossPosition, size, crossSize, theme)
		}
		position += size + base.gap
	}
}

func intrinsicCross(node Node, direction Direction, width, height int) int {
	if direction == ColumnLayout {
		return intrinsicMain(node, RowLayout, width, height)
	}
	return intrinsicMain(node, ColumnLayout, width, height)
}

func intrinsicMain(node Node, direction Direction, width, height int) int {
	base := node.nodeData()
	if direction == ColumnLayout {
		switch base.kind {
		case kindText, kindButton:
			return max(1, len(strings.Split(base.text, "\n")))
		case kindCanvas:
			return max(1, node.(*CanvasNode).height)
		default:
			return 1
		}
	}
	switch base.kind {
	case kindText:
		return max(1, longestLine(base.text))
	case kindButton:
		return max(3, len(base.text)+4)
	case kindCanvas:
		return max(1, node.(*CanvasNode).width)
	default:
		return 1
	}
}

func longestLine(value string) int {
	result := 0
	for _, line := range strings.Split(value, "\n") {
		if len([]rune(line)) > result {
			result = len([]rune(line))
		}
	}
	return result
}

func drawText(frame *Frame, x, y, width, height int, text string, style Style) {
	for lineIndex, line := range strings.Split(text, "\n") {
		if lineIndex >= height {
			break
		}
		if y+lineIndex < 0 || y+lineIndex >= frame.height {
			continue
		}
		runes := []rune(line)
		for offset := 0; offset < len(runes) && offset < width; offset++ {
			if x+offset < 0 || x+offset >= frame.width {
				continue
			}
			value := runes[offset]
			if !safeRune(value) {
				value = '\ufffd'
			}
			frame.cells[y+lineIndex][x+offset] = Cell{Rune: value, Style: style}
		}
	}
}

func safeRune(value rune) bool {
	return value >= 0x20 && value != 0x7f && !(value >= 0x80 && value <= 0x9f)
}

func drawBorder(frame *Frame, x, y, width, height int, border BorderStyle, style Style) {
	if width < 2 || height < 2 {
		return
	}
	left, right, bottomLeft, bottomRight := '+', '+', '+', '+'
	if border == Rounded {
		left, right, bottomLeft, bottomRight = '╭', '╮', '╰', '╯'
	}
	frame.cells[y][x] = Cell{Rune: left, Style: style}
	frame.cells[y][x+width-1] = Cell{Rune: right, Style: style}
	frame.cells[y+height-1][x] = Cell{Rune: bottomLeft, Style: style}
	frame.cells[y+height-1][x+width-1] = Cell{Rune: bottomRight, Style: style}
	for offset := 1; offset < width-1; offset++ {
		frame.cells[y][x+offset] = Cell{Rune: '─', Style: style}
		frame.cells[y+height-1][x+offset] = Cell{Rune: '─', Style: style}
	}
	for offset := 1; offset < height-1; offset++ {
		frame.cells[y+offset][x] = Cell{Rune: '│', Style: style}
		frame.cells[y+offset][x+width-1] = Cell{Rune: '│', Style: style}
	}
}

func drawCanvas(frame *Frame, node *CanvasNode, x, y, width, height int, style Style) {
	surface := &CanvasSurface{cells: frame, x: x, y: y, width: min(width, node.width), height: min(height, node.height), style: style}
	if node.draw != nil {
		node.draw(surface)
	}
}

func resolveStyle(style Style, theme Theme) Style {
	role, ok := theme.Roles[style.Role]
	if !ok {
		role = theme.Roles[Default]
	}
	if !style.HasForeground && role.HasForeground {
		style.Foreground, style.HasForeground = role.Foreground, true
	}
	if !style.HasBackground && role.HasBackground {
		style.Background, style.HasBackground = role.Background, true
	}
	style.Decorations |= role.Decorations
	return style
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
