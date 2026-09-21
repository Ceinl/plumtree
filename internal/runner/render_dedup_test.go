package runner

import (
	"bytes"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

type healthSink struct {
	frames  []abi.Frame
	healthy bool
	present bool
}

func (s *healthSink) Present(f abi.Frame) {
	s.present = true
	s.frames = append(s.frames, f)
}

func (s *healthSink) Healthy() bool { return s.healthy }

type failingWriter struct {
	failNext bool
	buf      bytes.Buffer
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.failNext {
		w.failNext = false
		return 0, bytes.ErrTooLarge
	}
	return w.buf.Write(p)
}

func TestFrameDedupSuppressesIdenticalFrames(t *testing.T) {
	d := &frameDedup{}
	sink := &healthSink{healthy: true}
	raw := []byte("frame-one")
	if d.suppressed(raw, sink) {
		t.Fatal("first frame suppressed")
	}
	d.observe(raw)
	if !d.suppressed(raw, sink) {
		t.Fatal("identical frame not suppressed")
	}
	other := []byte("frame-two")
	if d.suppressed(other, sink) {
		t.Fatal("changed frame suppressed")
	}
	// Observe copies: mutating the caller's raw must not corrupt the baseline.
	d.observe(raw)
	raw[0] = 'F'
	if !d.suppressed([]byte("frame-one"), sink) {
		t.Fatal("baseline mutated by observe")
	}
}

func TestFrameDedupRespectsSinkHealth(t *testing.T) {
	d := &frameDedup{}
	raw := []byte("frame")
	d.observe(raw)

	unhealthy := &healthSink{healthy: false}
	if d.suppressed(raw, unhealthy) {
		t.Fatal("suppressed while sink unhealthy")
	}
	// A sink without health reporting is never suppressed.
	if d.suppressed(raw, &capture{}) {
		t.Fatal("suppressed for sink without health")
	}
	recovered := &healthSink{healthy: true}
	if !d.suppressed(raw, recovered) {
		t.Fatal("not suppressed after sink recovered")
	}
}

// TestTTYSinkHealsAfterFailedWrite pins the dedup safety valve end to end: a
// failed flush makes Healthy false, so the identical frame is repainted and the
// screen converges instead of staying stale.
func TestTTYSinkHealsAfterFailedWrite(t *testing.T) {
	w := &failingWriter{failNext: true}
	sink := NewTTYSinkWriter(1, 1, 0, w)
	frame := abi.Frame{W: 1, H: 1, Cells: []abi.Cell{{Ch: 'A'}}}
	sink.Present(frame)
	if sink.Healthy() {
		t.Fatal("sink healthy after failed flush")
	}
	sink.Present(frame)
	if !sink.Healthy() {
		t.Fatal("sink did not heal on repaint")
	}
	// The first flush paints from an unstyled state: full reset, then colors.
	if got := w.buf.String(); got != "\x1b[1;1H\x1b[0m\x1b[48;2;25;23;29m\x1b[38;2;200;200;200mA" {
		t.Fatalf("healing flush wrote %q", got)
	}
}
