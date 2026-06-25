package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/spike/abi"
)

func TestSanitizeRuneStripsControls(t *testing.T) {
	for _, r := range []rune{0, 0x07, 0x1b, '\n', '\r', 0x7f, 0x85, 0x9b} {
		if got := sanitizeRune(r); got != ' ' {
			t.Errorf("sanitizeRune(%#x) = %q, want space", r, got)
		}
	}
	for _, r := range []rune{'a', 'Z', '7', '世', '↑', '─'} {
		if got := sanitizeRune(r); got != r {
			t.Errorf("sanitizeRune(%q) = %q, want unchanged", r, got)
		}
	}
}

// A hostile guest cannot drive the viewer's terminal: even a frame made
// entirely of escape/control runes renders with no escape bytes in the output.
func TestRenderTextDropsGuestEscapes(t *testing.T) {
	hostile := []rune{0x1b, '[', '2', 'J', 0x07, 0x9b, 0x00}
	f := abi.Frame{W: len(hostile), H: 1, Cells: make([]abi.Cell, len(hostile))}
	for i, r := range hostile {
		f.Cells[i] = abi.Cell{Ch: r}
	}
	var buf bytes.Buffer
	renderText(&buf, f)
	if bytes.ContainsRune(buf.Bytes(), 0x1b) || bytes.ContainsRune(buf.Bytes(), 0x07) {
		t.Errorf("rendered output leaked control bytes: %q", buf.String())
	}
}

func TestThrottleCoalesces(t *testing.T) {
	thr := newThrottle(10) // min 100ms between frames
	base := time.Unix(0, 0)
	if !thr.allow(base) {
		t.Fatal("first frame should be allowed")
	}
	if thr.allow(base.Add(50 * time.Millisecond)) {
		t.Error("frame within min interval should be coalesced")
	}
	if !thr.allow(base.Add(120 * time.Millisecond)) {
		t.Error("frame after min interval should be allowed")
	}
}

// End-to-end: instantiate the real guest and drive it through the ABI. Skips if
// the artifact has not been built.
func TestGuestCounterEndToEnd(t *testing.T) {
	wasm := readGuest(t)
	ctx := context.Background()
	sess, err := NewSession(ctx, wasm, Limits{MemoryPages: 512, FrameTimeout: 2 * time.Second}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close(ctx)

	const w, h = 30, 7
	mustFrame := func(ev *abi.Event) abi.Frame {
		t.Helper()
		var b []byte
		if ev != nil {
			b = abi.EncodeEvent(*ev)
		}
		f, err := sess.Frame(ctx, w, h, b)
		if err != nil {
			t.Fatalf("Frame: %v", err)
		}
		if f.W != w || f.H != h {
			t.Fatalf("frame dims = %dx%d, want %dx%d", f.W, f.H, w, h)
		}
		return f
	}

	if got := frameText(mustFrame(nil)); !strings.Contains(got, "Count: 0") {
		t.Errorf("initial frame missing Count: 0:\n%s", got)
	}
	up := abi.Event{Kind: abi.KindKey, Key: abi.KeyArrowUp}
	mustFrame(&up)
	if got := frameText(mustFrame(&up)); !strings.Contains(got, "Count: 2") {
		t.Errorf("after two ups, want Count: 2:\n%s", got)
	}
	down := abi.Event{Kind: abi.KindKey, Key: abi.KeyArrowDown}
	if got := frameText(mustFrame(&down)); !strings.Contains(got, "Count: 1") {
		t.Errorf("after down, want Count: 1:\n%s", got)
	}

	// Quit flag propagates from guest to host.
	q := abi.Event{Kind: abi.KindKey, Key: abi.KeyRune, Ch: 'q'}
	if f := mustFrame(&q); !f.Quit {
		t.Error("'q' should set the frame Quit flag")
	}
}

// A runaway guest is force-terminated at the per-frame deadline.
func TestRunawayGuestIsCancelled(t *testing.T) {
	wasm := readGuest(t)
	ctx := context.Background()
	sess, err := NewSession(ctx, wasm, Limits{MemoryPages: 512, FrameTimeout: 200 * time.Millisecond}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close(ctx)

	start := time.Now()
	b := abi.EncodeEvent(abi.Event{Kind: abi.KindKey, Key: abi.KeyRune, Ch: 'b'})
	_, err = sess.Frame(ctx, 20, 5, b)
	if err != ErrFrameDeadline {
		t.Fatalf("runaway frame err = %v, want ErrFrameDeadline", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancellation took %s, deadline was 200ms", elapsed)
	}
	// Session must be unusable after a deadline kill.
	if _, err := sess.Frame(ctx, 20, 5, nil); err != ErrFrameDeadline {
		t.Errorf("post-kill frame err = %v, want ErrFrameDeadline", err)
	}
}

// frameText flattens a frame's sanitized glyphs into newline-joined rows.
func frameText(f abi.Frame) string {
	var b strings.Builder
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			b.WriteRune(sanitizeRune(f.At(x, y).Ch))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func readGuest(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../dist/counter.wasm")
	if err != nil {
		t.Skip("dist/counter.wasm not built; run ./build.sh")
	}
	return b
}
