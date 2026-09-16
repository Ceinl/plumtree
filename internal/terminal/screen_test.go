package terminal

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

type frameWriter struct {
	bytes.Buffer
	calls int
	fail  bool
}

func (w *frameWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	return w.Buffer.Write(p)
}

func TestFlushBatchesChangesAndResetsDecorations(t *testing.T) {
	w := &frameWriter{}
	s := NewScreenWithOutput(3, 1, w)
	s.Set(0, 0, abi.Cell{Ch: 'λ', Decor: abi.DecorBold})
	s.Set(1, 0, abi.Cell{Ch: 'x', Fg: abi.RGB{R: 12, G: 34, B: 56}})
	s.Flush()
	if w.calls != 1 {
		t.Fatalf("writes = %d, want one frame", w.calls)
	}
	if !strings.Contains(w.String(), "λ") || !strings.Contains(w.String(), "\x1b[1;2H\x1b[0m") || !strings.Contains(w.String(), "\x1b[38;2;12;34;56m") {
		t.Fatalf("incorrect output: %q", w.String())
	}
	s.Flush()
	if w.calls != 1 {
		t.Fatal("unchanged screen produced output")
	}
	w.Reset()
	s.Resize(1, 1)
	s.Set(0, 0, abi.Cell{Ch: 'z'})
	s.Flush()
	if !strings.Contains(w.String(), "z") {
		t.Fatal("resize did not repaint")
	}
}

func TestFlushRetriesAfterWriteFailure(t *testing.T) {
	w := &frameWriter{fail: true}
	s := NewScreenWithOutput(1, 1, w)
	s.Set(0, 0, abi.Cell{Ch: 'x'})
	s.Flush()
	w.fail = false
	s.Flush()
	if !strings.Contains(w.String(), "x") {
		t.Fatal("failed output was marked as painted")
	}
}

func TestFlushOmitsRepeatedStylesAndAdjacentASCIICursors(t *testing.T) {
	w := &frameWriter{}
	s := NewScreenWithOutput(3, 1, w)
	for x := 0; x < 3; x++ {
		s.Set(x, 0, abi.Cell{Ch: rune('a' + x), Fg: abi.RGB{R: uint8(x + 1)}, Bg: abi.RGB{B: 10}})
	}
	s.Flush()
	if strings.Count(w.String(), "H") != 1 || strings.Count(w.String(), "\x1b[48;") != 1 || strings.Count(w.String(), "\x1b[0m") != 1 {
		t.Fatalf("repeated state: %q", w.String())
	}
	// A wide glyph must not let the encoder assume the next cell is adjacent.
	w.Reset()
	s.Set(0, 0, abi.Cell{Ch: '界'})
	s.Set(1, 0, abi.Cell{Ch: 'z', Fg: abi.RGB{R: 30}})
	s.Flush()
	if !strings.Contains(w.String(), "\x1b[1;2H") {
		t.Fatalf("missing explicit position after Unicode: %q", w.String())
	}
}

func TestFlushRetriesDirtyRowsAfterShortWrite(t *testing.T) {
	var out bytes.Buffer
	s := NewScreenWithOutput(8, 3, &out)
	s.Flush()
	out.Reset()
	for _, y := range []int{0, 2} {
		s.Set(7, y, abi.Cell{Ch: 'X'})
	}
	s.out = shortFrameWriter{}
	s.Flush()
	if s.Healthy() {
		t.Fatal("short write reported healthy")
	}
	for _, y := range []int{0, 2} {
		if s.old[y][7].Ch != ' ' {
			t.Fatal("failed write committed a dirty row")
		}
	}
	s.out = &out
	s.Flush()
	if !s.Healthy() || strings.Count(out.String(), "X") != 2 {
		t.Fatalf("retry did not repaint both dirty rows: %q", out.String())
	}
	out.Reset()
	s.Flush()
	if out.Len() != 0 {
		t.Fatal("successful retry did not commit dirty rows")
	}
}

type shortFrameWriter struct{}

func (shortFrameWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestFlushLargeCoordinates(t *testing.T) {
	for _, size := range [][2]int{{255, 1}, {256, 1}, {MaxWidth, 1}, {1, 255}, {1, 256}, {1, MaxHeight}, {MaxWidth, MaxHeight}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			var out bytes.Buffer
			s := NewScreenWithOutput(1, 1, &out)
			s.Flush()
			s.Resize(w, h)
			s.Flush()
			out.Reset()
			s.Set(w-1, h-1, abi.Cell{Ch: 'X'})
			s.Flush()
			want := fmt.Sprintf("\x1b[%d;%dH", h, w)
			if !strings.HasPrefix(out.String(), want) || !strings.HasSuffix(out.String(), "X") {
				t.Fatalf("output = %q, want cursor %q followed by styled X", out.String(), want)
			}
			out.Reset()
			s.Flush()
			if out.Len() != 0 {
				t.Fatalf("unchanged screen produced output: %q", out.String())
			}
		})
	}
}

func TestFlushRepositionsWithinSameStyleAfterUnicode(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreenWithOutput(3, 1, &out)
	for x, ch := range []rune{'a', '界', 'z'} {
		screen.Set(x, 0, abi.Cell{Ch: ch})
	}
	screen.Flush()
	if !strings.Contains(out.String(), "a界\x1b[1;3Hz") {
		t.Fatalf("wrong cell position: %q", out.String())
	}
}
