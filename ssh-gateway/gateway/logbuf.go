package gateway

import "bytes"

// maxSessionLogBytes caps how much guest stdout/stderr the gateway keeps per
// session. A noisy guest cannot grow this without bound; output past the cap is
// dropped and the session is marked truncated.
const maxSessionLogBytes = 64 << 10

// capWriter buffers up to a fixed number of bytes and silently drops the rest,
// recording that it did. Writes never error, so a guest flooding stdout is
// neither blocked nor failed — its excess output is simply not retained.
type capWriter struct {
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func newCapWriter(capBytes int) *capWriter { return &capWriter{cap: capBytes} }

func (w *capWriter) Write(p []byte) (int, error) {
	if remaining := w.cap - w.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			w.buf.Write(p[:remaining])
			w.truncated = true
		} else {
			w.buf.Write(p)
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	return len(p), nil
}

func (w *capWriter) String() string { return w.buf.String() }
