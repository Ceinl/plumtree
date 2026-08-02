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
	if value < 0 || value == '\x1b' {
		return false
	}
	surface.cells.cells[surface.y+y][surface.x+x] = Cell{Rune: value, Style: style}
	return true
}

// SetText writes clipped text beginning at x,y.
func (surface *CanvasSurface) SetText(x, y int, text string, style Style) int {
	count := 0
	for _, value := range text {
		if surface.Set(x, y, value, style) {
			count++
		}
		x++
	}
	return count
}

// Fill writes a bounded rectangle.
func (surface *CanvasSurface) Fill(x, y, width, height int, value rune, style Style) int {
	count := 0
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			if surface.Set(x+column, y+row, value, style) {
				count++
			}
		}
	}
	return count
}
