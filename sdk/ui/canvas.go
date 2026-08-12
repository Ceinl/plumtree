package ui

// CanvasSurface is a clipped structured drawing target. Coordinates outside
// the node's declared bounds are ignored, so custom drawing cannot write past
// the frame or emit terminal control sequences.
type CanvasSurface struct {
	cells         *Frame
	x, y          int
	width, height int
	style         Style
}

// Width returns the clipped drawing width.
func (surface *CanvasSurface) Width() int { return surface.width }

// Height returns the clipped drawing height.
func (surface *CanvasSurface) Height() int { return surface.height }

// Set writes one structured cell when coordinates are in bounds.
func (surface *CanvasSurface) Set(x, y int, value rune, style Style) bool {
	if surface == nil || surface.cells == nil || x < 0 || y < 0 || x >= surface.width || y >= surface.height {
		return false
	}
	if !safeRune(value) {
		return false
	}
	surface.cells.cells[surface.y+y][surface.x+x] = Cell{Rune: value, Style: style}
	return true
}

// SetText writes clipped text beginning at x,y.
func (surface *CanvasSurface) SetText(x, y int, text string, style Style) int {
	if surface == nil || y < 0 || y >= surface.height || x >= surface.width {
		return 0
	}
	values := []rune(text)
	start := 0
	if x < 0 {
		if x <= -len(values) {
			return 0
		}
		start = min(len(values), -x)
		x = 0
	}
	count := 0
	for _, value := range values[start:] {
		if x >= surface.width {
			break
		}
		if surface.Set(x, y, value, style) {
			count++
		}
		x++
	}
	return count
}

// Fill writes a bounded rectangle.
func (surface *CanvasSurface) Fill(x, y, width, height int, value rune, style Style) int {
	if surface == nil || width <= 0 || height <= 0 {
		return 0
	}
	left, top := max(0, x), max(0, y)
	right, bottom := min(surface.width, x+width), min(surface.height, y+height)
	count := 0
	for row := top; row < bottom; row++ {
		for column := left; column < right; column++ {
			if surface.Set(column, row, value, style) {
				count++
			}
		}
	}
	return count
}
