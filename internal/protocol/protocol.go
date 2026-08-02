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
)

// Op identifies one message in the lock-step worker protocol.
type Op byte

const (
	OpStart       Op = 1 // parent -> worker: limits + appType + wasm
	OpResp        Op = 2 // parent -> worker: reply to the previous request
	OpRecv        Op = 3 // worker -> parent: next input event
	OpPresent     Op = 4 // worker -> parent: a rendered frame
	OpKVGet       Op = 5
	OpKVSet       Op = 6
	OpKVDel       Op = 7
	OpBusSub      Op = 8
	OpBusPub      Op = 9
	OpAuth        Op = 10
	OpEnv         Op = 11
	OpFetch       Op = 12
	OpDone        Op = 13 // worker -> parent: session finished (err + logs)
	OpOutput      Op = 14
	OpKVList      Op = 15
	OpKVCAS       Op = 16
	OpExec        Op = 17
	OpTimerStart  Op = 18
	OpTimerCancel Op = 19
)

// MaxFrame bounds a single protocol message before any payload allocation.
const MaxFrame = 64 << 20 // 64 MiB; a WASM module fits, frames are far smaller

// ErrProtocol reports malformed, unknown, or oversized protocol data.
var ErrProtocol = errors.New("runner: protocol error")

// Write writes one framed message: [op][u32 little-endian length][payload].
func Write(w io.Writer, op Op, payload []byte) error {
	if len(payload) > MaxFrame {
		return ErrProtocol
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

// ReadBounded rejects an operation-specific oversized message before
// allocating its payload. A nil maxFor applies only the global MaxFrame cap.
func ReadBounded(r io.Reader, maxFor func(Op) uint32) (Op, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	op := Op(hdr[0])
	n := binary.LittleEndian.Uint32(hdr[1:])
	if n > MaxFrame {
		return 0, nil, ErrProtocol
	}
	if maxFor != nil && n > maxFor(op) {
		return 0, nil, ErrProtocol
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return op, payload, nil
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
