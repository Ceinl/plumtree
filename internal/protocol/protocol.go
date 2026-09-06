// Package protocol owns the bounded framing used by the isolated runner.
//
// The package deliberately knows nothing about runner capabilities or guest
// payloads. It provides only the neutral operation byte, little-endian frame
// format, and allocation guard shared by the worker and its parent.
package protocol

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

// Op identifies one message in the lock-step worker protocol.
type Op byte

const (
	OpStart        Op = 1 // parent -> worker: limits + appType + wasm
	OpResp         Op = 2 // parent -> worker: reply to the previous request
	OpRecv         Op = 3 // worker -> parent: next input event
	OpPresent      Op = 4 // worker -> parent: a rendered frame
	OpKVGet        Op = 5
	OpKVSet        Op = 6
	OpKVDel        Op = 7
	OpBusSub       Op = 8
	OpBusPub       Op = 9
	OpAuth         Op = 10
	OpEnv          Op = 11
	OpFetch        Op = 12
	OpDone         Op = 13 // worker -> parent: session finished (err + logs)
	OpOutput       Op = 14
	OpKVList       Op = 15
	OpKVCAS        Op = 16
	OpExec         Op = 17
	OpTimerStart   Op = 18
	OpTimerCancel  Op = 19
	OpOutputStderr Op = 20 // worker -> parent: stderr output
	OpInput        Op = 21 // worker -> parent: request stdin bytes
)

// MaxFrame bounds a single protocol message before any payload allocation.
const MaxFrame = 64 << 20 // 64 MiB; a WASM module fits, frames are far smaller

// inlineWriteMax is the payload size under which the header and payload are
// coalesced into one Write. Every frame below this size (event replies, kv
// results, statuses) costs one write call instead of two; larger payloads
// (frames, WASM modules) skip the copy and write the payload directly.
const inlineWriteMax = 4096

// The inline buffer escapes through the io.Writer interface call, so it cannot
// live on the stack; pooling keeps the coalesced write allocation-free.
var inlineBufPool = sync.Pool{New: func() any { return new([5 + inlineWriteMax]byte) }}

// ErrProtocol reports malformed, unknown, or oversized protocol data.
var ErrProtocol = errors.New("runner: protocol error")

// Write writes one framed message: [op][u32 little-endian length][payload].
func Write(w io.Writer, op Op, payload []byte) error {
	if len(payload) > MaxFrame {
		return ErrProtocol
	}
	if len(payload) <= inlineWriteMax {
		bufp := inlineBufPool.Get().(*[5 + inlineWriteMax]byte)
		defer inlineBufPool.Put(bufp)
		b := bufp[:5+len(payload)]
		b[0] = byte(op)
		binary.LittleEndian.PutUint32(b[1:5], uint32(len(payload)))
		copy(b[5:], payload)
		return writeAll(w, b)
	}
	var hdr [5]byte
	hdr[0] = byte(op)
	binary.LittleEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if err := writeAll(w, hdr[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

// Read reads one frame, bounded by MaxFrame.
func Read(r io.Reader) (Op, []byte, error) {
	return ReadBounded(r, func(Op) uint32 { return MaxFrame })
}

// AppendRead reads one frame into buf, reusing buf's storage when it is large
// enough. The returned payload aliases buf and is valid only until the next
// AppendRead on the same buffer.
func AppendRead(r io.Reader, buf []byte) (Op, []byte, error) {
	return AppendReadBounded(r, buf, nil)
}

// ReadBounded rejects an operation-specific oversized message before
// allocating its payload. A nil maxFor applies only the global MaxFrame cap.
func ReadBounded(r io.Reader, maxFor func(Op) uint32) (Op, []byte, error) {
	return AppendReadBounded(r, nil, maxFor)
}

// AppendReadBounded is ReadBounded with a reusable payload buffer. The
// returned payload aliases buf (or a fresh allocation when buf is too small);
// it is valid only until the next read on the same buffer.
func AppendReadBounded(r io.Reader, buf []byte, maxFor func(Op) uint32) (Op, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, buf, err
	}
	op := Op(hdr[0])
	n := binary.LittleEndian.Uint32(hdr[1:])
	if n > MaxFrame {
		return 0, buf, ErrProtocol
	}
	if maxFor != nil && n > maxFor(op) {
		return 0, buf, ErrProtocol
	}
	if uint64(n) > uint64(cap(buf)) {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, buf, err
	}
	return op, buf, nil
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
