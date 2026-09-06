package terminal

import (
	"bytes"
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
