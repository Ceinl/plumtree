package protocol

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestReadBoundedRejectsBeforeAllocation(t *testing.T) {
	var frame bytes.Buffer
	frame.WriteByte(byte(OpPresent))
	frame.Write([]byte{0xff, 0xff, 0xff, 0x7f})

	_, _, err := ReadBounded(&frame, func(Op) uint32 { return 4 })
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("ReadBounded error = %v, want %v", err, ErrProtocol)
	}
}

func TestWriteHandlesShortWriters(t *testing.T) {
	w := &shortWriter{max: 1}
	if err := Write(w, OpResp, []byte("reply")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	op, payload, err := Read(bytes.NewReader(w.buf))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if op != OpResp || string(payload) != "reply" {
		t.Fatalf("frame = (%d, %q), want (%d, reply)", op, payload, OpResp)
	}
}

type shortWriter struct {
	buf []byte
	max int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	n := min(len(p), w.max)
	w.buf = append(w.buf, p[:n]...)
	return n, nil
}

var _ io.Writer = (*shortWriter)(nil)
