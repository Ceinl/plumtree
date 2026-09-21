package terminal

import (
	"bytes"
	"io"
	"math"
	"reflect"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func gridCopy(s *Screen) [][]abi.Cell {
	out := make([][]abi.Cell, len(s.cur))
	for y := range s.cur {
		out[y] = append([]abi.Cell(nil), s.cur[y]...)
	}
	return out
}

func renderLine(s *Screen, y int) string {
	if y < 0 || y >= s.h {
		return ""
	}
	var buf bytes.Buffer
	for _, cell := range s.cur[y] {
		buf.WriteRune(cell.Ch)
	}
	return buf.String()
}

func assertGridInvariants(t *testing.T, s *Screen) {
	t.Helper()
	if s.w < MinWidth || s.w > MaxWidth || s.h < MinHeight || s.h > MaxHeight {
		t.Fatalf("grid %dx%d outside %dx%d..%dx%d", s.w, s.h, MinWidth, MinHeight, MaxWidth, MaxHeight)
	}
	if s.w*s.h > MaxCells {
		t.Fatalf("grid area %d exceeds %d", s.w*s.h, MaxCells)
	}
	if len(s.cur) != s.h || len(s.old) != s.h {
		t.Fatalf("row counts = %d/%d, want %d", len(s.cur), len(s.old), s.h)
	}
	for y := range s.cur {
		if len(s.cur[y]) != s.w || len(s.old[y]) != s.w {
			t.Fatalf("row %d width = %d/%d, want %d", y, len(s.cur[y]), len(s.old[y]), s.w)
		}
	}
}

// Every invalid viewport dimension clamps into one safe, fully allocated
// grid: no panic, no partial allocation, no oversized buffer.
func TestHostileDimensionsClampIntoSafeGrid(t *testing.T) {
	for _, test := range []struct {
		name         string
		w, h         int
		wantW, wantH int
	}{
		{"negative", -5, -3, MinWidth, MinHeight},
		{"zero", 0, 0, MinWidth, MinHeight},
		{"over-maximum", MaxWidth + 1, MaxHeight + 1, MaxWidth, MaxHeight},
		{"oversized", 1 << 40, 1 << 40, MaxWidth, MaxHeight},
		{"int-max-wide", int(^uint(0) >> 1), 1, MaxWidth, 1},
		{"int-max-tall", 1, int(^uint(0) >> 1), 1, MaxHeight},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := NewScreenWithOutput(test.w, test.h, io.Discard)
			if s.w != test.wantW || s.h != test.wantH {
				t.Fatalf("grid = %dx%d, want %dx%d", s.w, s.h, test.wantW, test.wantH)
			}
			assertGridInvariants(t, s)
		})
	}
}

// Out-of-bounds Set is ignored exactly; the grid stays untouched.
func TestSetOutsideGridNeverTouchesCells(t *testing.T) {
	s := NewScreenWithOutput(4, 2, io.Discard)
	before := gridCopy(s)
	for _, coord := range [][2]int{
		{-1, 0}, {4, 0}, {0, -1}, {0, 2}, {1 << 40, 1 << 40},
		{int(^uint(0) >> 1), 0}, {0, int(^uint(0) >> 1)},
	} {
		assertNotPanics(t, func() { s.Set(coord[0], coord[1], abi.Cell{Ch: 'm'}) })
	}
	if !reflect.DeepEqual(before, gridCopy(s)) {
		t.Fatal("out-of-bounds Set changed the grid")
	}
}

// SetRow clips at the screen width, leaves absent cells untouched, and
// rejects negative and oversized row indices.
func TestSetRowClipsWithoutCorruptingOtherRows(t *testing.T) {
	s := NewScreenWithOutput(4, 2, io.Discard)
	before := gridCopy(s)
	s.SetRow(-1, []abi.Cell{{Ch: 'x'}, {Ch: 'x'}, {Ch: 'x'}, {Ch: 'x'}})
	s.SetRow(2, []abi.Cell{{Ch: 'x'}, {Ch: 'x'}, {Ch: 'x'}, {Ch: 'x'}})
	if !reflect.DeepEqual(before, gridCopy(s)) {
		t.Fatal("rejected SetRow changed the grid")
	}
	s.SetRow(0, []abi.Cell{{Ch: 'a'}, {Ch: 'b'}})
	s.SetRow(1, []abi.Cell{{Ch: 'c'}, {Ch: 'd'}, {Ch: 'e'}, {Ch: 'f'}, {Ch: 'g'}, {Ch: 'h'}})
	s.Flush()
	if got := renderLine(s, 0); got != "ab  " {
		t.Fatalf("partial row stored %q, want untouched tail", got)
	}
	if got := renderLine(s, 1); got != "cdef" {
		t.Fatalf("oversized row stored %q, want clipped %q", got, "cdef")
	}
	if before := renderLine(s, 0); before == "xxxx" {
		t.Fatal("clipping check is unsound")
	}
}

// Hostile resizes keep the grid valid and flushable: no panic, no partial
// allocation, and the emitter stays consistent with the new grid.
func TestHostileResizesStaySafe(t *testing.T) {
	w := &frameWriter{}
	s := NewScreenWithOutput(4, 2, w)
	for _, size := range [][2]int{{-7, -7}, {1 << 40, 1 << 40}, {MaxWidth, MaxHeight}} {
		s.Resize(size[0], size[1])
		s.Set(0, 0, abi.Cell{Ch: 'o'})
		s.Flush()
		assertGridInvariants(t, s)
	}
}

func assertNotPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("renderer panicked: %v", r)
		}
	}()
	fn()
}

// FuzzScreenCoordinates drives the renderer at and beyond every coordinate
// and dimension boundary: the grid always clamps into the safe rectangle,
// out-of-bounds operations are ignored, and buffer access never panics.
func FuzzScreenCoordinates(f *testing.F) {
	for _, size := range [][2]int{{-3, -1}, {0, 0}, {1, 1}, {2, 3}, {MaxWidth + 1, MaxHeight + 1}, {1 << 30, 1 << 20}, {math.MaxInt, math.MaxInt}} {
		for _, coord := range [][2]int{{-1, -1}, {0, 0}, {1, 1}, {MaxWidth, MaxHeight}, {1 << 30, 1 << 30}} {
			f.Add(size[0], size[1], coord[0], coord[1], uint(0))
			f.Add(size[0], size[1], coord[0], coord[1], uint(5))
		}
	}
	f.Fuzz(func(t *testing.T, w, h, x, y int, rowLen uint) {
		// Resize first so the write attempts below often land in-bounds.
		assertNotPanics(t, func() {
			s := NewScreenWithOutput(w, h, io.Discard)
			s.Resize(x, y)
			s.Set(x, y, abi.Cell{Ch: 'f'})
			cells := make([]abi.Cell, rowLen%16)
			for i := range cells {
				cells[i] = abi.Cell{Ch: 'r'}
			}
			s.SetRow(y, cells)
			s.Flush()
			// A successful flush (discard writes cannot fail) leaves the
			// previous frame exactly equal to the current one.
			if s.failed {
				t.Errorf("flush to discard reported a write failure")
			}
			for row := range s.cur {
				if !reflect.DeepEqual(s.old[row], s.cur[row]) {
					t.Errorf("flush left old row %d different from cur", row)
				}
			}
			assertGridInvariants(t, s)
		})
	})
}
