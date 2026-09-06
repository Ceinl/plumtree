package ui

// Renderer reuses cell and hit-region storage between renders. Its zero value
// is ready to use. It must not be used concurrently. Returned frames borrow
// its storage until the next render; use Frame.Clone to retain a snapshot.
type Renderer struct {
	frame Frame
	cells []Cell
}

// Render lays out and draws node using DefaultTheme and reusable storage.
func (r *Renderer) Render(node Node, width, height int) Frame {
	return r.RenderWithTheme(node, width, height, DefaultTheme())
}

// RenderWithTheme renders with a caller-provided theme and reusable storage.
func (r *Renderer) RenderWithTheme(node Node, width, height int, theme Theme) Frame {
	width, height = max(0, width), max(0, height)
	n := width * height
	if cap(r.cells) < n {
		r.cells = make([]Cell, n)
	} else {
		r.cells = r.cells[:n]
		clear(r.cells)
	}
	for i := range r.cells {
		r.cells[i].Rune = ' '
	}
	// Clear old row references so a smaller viewport does not retain a grid
	// replaced by a later allocation. Rows share one contiguous cell buffer.
	clear(r.frame.cells)
	if cap(r.frame.cells) < height {
		r.frame.cells = make([][]Cell, height)
	} else {
		r.frame.cells = r.frame.cells[:height]
	}
	for y := range r.frame.cells {
		start, end := y*width, (y+1)*width
		r.frame.cells[y] = r.cells[start:end:end]
	}
	// Hit regions retain nodes, so release references before reusing the slice.
	clear(r.frame.hits)
	r.frame.hits = r.frame.hits[:0]
	r.frame.width, r.frame.height, r.frame.root = width, height, node
	if node != nil && width > 0 && height > 0 {
		renderNode(&r.frame, node, 0, 0, width, height, theme)
	}
	return r.frame
}
