package screen

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewScreenDimensions(t *testing.T) {
	s := NewScreen(10, 5)
	if s.Width() != 10 || s.Height() != 5 {
		t.Errorf("got %dx%d, want 10x5", s.Width(), s.Height())
	}
}

func TestScreenDimensionsAreBoundedBeforeAllocation(t *testing.T) {
	s := NewScreen(MaxWidth+1, MaxHeight+1)
	if s.Width() != MaxWidth || s.Height() != MaxHeight {
		t.Fatalf("new screen = %dx%d, want bounded %dx%d", s.Width(), s.Height(), MaxWidth, MaxHeight)
	}
	if got := s.Width() * s.Height(); got > MaxCells {
		t.Fatalf("new screen allocated %d cells, max is %d", got, MaxCells)
	}

	s.Resize(-1, int(^uint(0)>>1))
	if !ValidDimensions(s.Width(), s.Height()) {
		t.Fatalf("resized screen has invalid dimensions %dx%d", s.Width(), s.Height())
	}
	if got := s.Width() * s.Height(); got > MaxCells {
		t.Fatalf("resized screen allocated %d cells, max is %d", got, MaxCells)
	}
}

func TestValidDimensions(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		want bool
	}{
		{1, 1, true},
		{MaxWidth, MaxHeight, true},
		{0, 1, false},
		{1, 0, false},
		{MaxWidth + 1, 1, false},
		{1, MaxHeight + 1, false},
	} {
		if got := ValidDimensions(tc.w, tc.h); got != tc.want {
			t.Errorf("ValidDimensions(%d, %d) = %v, want %v", tc.w, tc.h, got, tc.want)
		}
	}
}

func TestSetAndClear(t *testing.T) {
	s := NewScreen(4, 2)
	s.Set(1, 1, 'x', "fg", "bg", "")
	if got := s.cur[1][1]; got.Ch != 'x' || got.Fg != "fg" || got.Bg != "bg" {
		t.Errorf("cell = %+v", got)
	}
	s.Set(0, 0, 'y', "", "", "")
	if got := s.cur[0][0]; got.Fg != DefaultFg || got.Bg != DefaultBg {
		t.Errorf("empty fg/bg should fall back to defaults, got %+v", got)
	}
	s.Clear()
	if got := s.cur[1][1]; got.Ch != ' ' || got.Bg != DefaultBg {
		t.Errorf("after Clear cell = %+v", got)
	}
}

func TestSnapshotCopiesGrid(t *testing.T) {
	s := NewScreen(3, 2)
	s.Set(2, 1, 'z', "fg", "bg", "d")

	snap := s.Snapshot()
	if len(snap) != 2 || len(snap[0]) != 3 {
		t.Fatalf("snapshot dims = %dx%d, want 3x2", len(snap[0]), len(snap))
	}
	if got := snap[1][2]; got.Ch != 'z' || got.Fg != "fg" || got.Bg != "bg" || got.Decor != "d" {
		t.Errorf("snapshot cell = %+v", got)
	}
	if got := snap[0][0]; got.Ch != ' ' || got.Bg != DefaultBg {
		t.Errorf("snapshot default cell = %+v", got)
	}

	// Mutating the snapshot must not affect the live buffer.
	snap[1][2] = Cell{Ch: '!'}
	if s.cur[1][2].Ch != 'z' {
		t.Errorf("snapshot aliases live buffer: cur=%+v", s.cur[1][2])
	}
}

func TestSetOutOfBoundsIsIgnored(t *testing.T) {
	s := NewScreen(2, 2)
	s.Set(-1, 0, 'a', "", "", "")
	s.Set(0, -1, 'a', "", "", "")
	s.Set(2, 0, 'a', "", "", "")
	s.Set(0, 2, 'a', "", "", "")
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			if s.cur[y][x].Ch != ' ' {
				t.Errorf("cell (%d,%d) modified by out-of-bounds Set", x, y)
			}
		}
	}
}

func TestFlushWritesOnlyChangedCells(t *testing.T) {
	var buf bytes.Buffer
	s := NewScreen(3, 1)
	s.out = &buf

	s.Set(0, 0, 'h', "", "", "")
	s.Set(1, 0, 'i', "", "", "")
	s.Flush()
	first := buf.String()
	if !strings.Contains(first, "hi") {
		t.Errorf("first flush missing content: %q", first)
	}

	buf.Reset()
	s.Flush()
	second := buf.String()
	if strings.Contains(second, "hi") {
		t.Errorf("second flush should not redraw unchanged cells: %q", second)
	}
}

func TestFlushDisablesAutowrap(t *testing.T) {
	// Painting the last column of the last row must not be allowed to scroll the
	// terminal, so every frame disables autowrap (DECAWM) while it paints and
	// restores it afterwards. Without this the screen creeps/flickers.
	var buf bytes.Buffer
	s := NewScreen(2, 1)
	s.out = &buf
	s.Set(1, 0, 'x', "", "", "")
	s.Flush()
	out := buf.String()
	if !strings.Contains(out, "\x1b[?7l") {
		t.Errorf("flush should disable autowrap: %q", out)
	}
	if !strings.Contains(out, "\x1b[?7h") {
		t.Errorf("flush should restore autowrap: %q", out)
	}
	if strings.Index(out, "\x1b[?7l") > strings.Index(out, "\x1b[?7h") {
		t.Errorf("autowrap must be disabled before it is restored: %q", out)
	}
}

func TestSetCursorClamps(t *testing.T) {
	var buf bytes.Buffer
	s := NewScreen(4, 3)
	s.out = &buf
	s.SetCursor(99, 99)
	if got, want := buf.String(), "\x1b[3;4H"; got != want {
		t.Errorf("SetCursor(99,99) wrote %q, want %q", got, want)
	}
	buf.Reset()
	s.SetCursor(-5, -5)
	if got, want := buf.String(), "\x1b[1;1H"; got != want {
		t.Errorf("SetCursor(-5,-5) wrote %q, want %q", got, want)
	}
}
