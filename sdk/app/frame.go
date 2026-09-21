package app

import (
	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/ui"
)

// appendFrame encodes under the runtime lock, avoiding the public Frame copy.
// Only the encoded buffer crosses the host call; model state stays private.
func (r *Runtime) appendFrame(dst []byte, converter *cleanFrameConverter) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return abi.AppendFrame(dst, converter.frame(r.frame, r.quit))
}

type cleanFrameConverter struct{ cells []abi.Cell }

func (converter *cleanFrameConverter) frame(frame ui.Frame, quit bool) abi.Frame {
	w, h := frame.Width(), frame.Height()
	n := w * h
	if cap(converter.cells) < n {
		converter.cells = make([]abi.Cell, n)
	} else {
		converter.cells = converter.cells[:n]
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cell, ok := frame.Cell(x, y)
			if !ok {
				continue
			}
			fg := cell.Style.Foreground
			bg := cell.Style.Background
			if !cell.Style.HasForeground {
				fg = ui.RGB(200, 200, 200)
			}
			if !cell.Style.HasBackground {
				bg = ui.RGB(25, 23, 29)
			}
			var decor uint8
			if cell.Style.Decorations&ui.Bold != 0 {
				decor |= abi.DecorBold
			}
			if cell.Style.Decorations&ui.Underline != 0 {
				decor |= abi.DecorUnderline
			}
			converter.cells[y*w+x] = abi.Cell{Ch: cell.Rune, Fg: abi.RGB{R: fg.R, G: fg.G, B: fg.B}, Bg: abi.RGB{R: bg.R, G: bg.G, B: bg.B}, Decor: decor}
		}
	}
	return abi.Frame{W: w, H: h, Quit: quit, Cells: converter.cells}
}
