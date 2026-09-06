package keyboard

import (
	"context"
	"io"
	"os"
	"time"
	"unicode/utf8"
)

type Event struct {
	Type                  EventType
	Ch                    rune
	Shift, Ctrl, Alt, Cmd bool
	Mouse                 bool
	MouseX, MouseY        int
}

type EventType int

const (
	KeyRune EventType = iota
	KeyEnter
	KeyBackspace
	KeyCtrlC
	KeyTab
	KeyEscape
	KeyArrowUp
	KeyArrowDown
	KeyArrowRight
	KeyArrowLeft
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyDelete
	KeyMouseWheelUp
	KeyMouseWheelDown
	KeyMouseLeftDown
	KeyMouseLeftDrag
	KeyMouseLeftUp
	KeyUnknown
)

// chunkLen bounds one read's worth of forwarded input. Keystrokes arrive in
// bursts (a whole escape sequence, a paste), so one channel send per chunk
// replaces the per-byte send of the naive design.
const chunkLen = 4096

func Listen(ctx context.Context) <-chan Event { return ListenReader(ctx, os.Stdin) }

func ListenReader(ctx context.Context, input io.Reader) <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		chunks := make(chan []byte, 4)
		go func() {
			defer close(chunks)
			buf := make([]byte, chunkLen)
			for {
				n, err := input.Read(buf)
				if n > 0 {
					// The read buffer is reused, so the chunk gets its own copy.
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					select {
					case chunks <- chunk:
					case <-ctx.Done():
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
		s := &byteStream{ctx: ctx, chunks: chunks}
		for {
			b, ok := s.next()
			if !ok {
				return
			}
			event := s.parse(b)
			if event.Type == KeyUnknown {
				continue
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// byteStream feeds the parser from queued chunks, preserving the old
// read-continuation semantics: bytes already read are consumed immediately,
// while a sequence split across reads waits up to continuationTimeout.
type byteStream struct {
	ctx    context.Context
	chunks <-chan []byte
	cur    []byte
}

func (s *byteStream) next() (byte, bool) {
	if len(s.cur) > 0 {
		b := s.cur[0]
		s.cur = s.cur[1:]
		return b, true
	}
	select {
	case chunk, ok := <-s.chunks:
		if !ok {
			return 0, false
		}
		s.cur = chunk[1:]
		return chunk[0], true
	case <-s.ctx.Done():
		return 0, false
	}
}

func (s *byteStream) parse(b byte) Event {
	switch b {
	case 3:
		return Event{Type: KeyCtrlC, Ctrl: true}
	case 9:
		return Event{Type: KeyTab}
	case 10, 13:
		return Event{Type: KeyEnter}
	case 127:
		return Event{Type: KeyBackspace}
	case 27:
		b1, ok := s.continuation()
		if !ok {
			return Event{Type: KeyEscape}
		}
		if b1 != '[' {
			return Event{Type: KeyUnknown}
		}
		return readCSI(s.continuation)
	default:
		if b < utf8.RuneSelf {
			return Event{Type: KeyRune, Ch: rune(b)}
		}
		var raw [utf8.UTFMax]byte
		raw[0] = b
		n := 1
		for !utf8.FullRune(raw[:n]) && n < len(raw) {
			next, ok := s.continuation()
			if !ok {
				return Event{Type: KeyUnknown}
			}
			raw[n] = next
			n++
		}
		ch, _ := utf8.DecodeRune(raw[:n])
		return Event{Type: KeyRune, Ch: ch}
	}
}

// continuation reads the next byte of a multi-byte sequence. Split input waits
// up to continuationTimeout; already-buffered bytes return without a timer.
func (s *byteStream) continuation() (byte, bool) {
	if len(s.cur) > 0 {
		b := s.cur[0]
		s.cur = s.cur[1:]
		return b, true
	}
	select {
	case chunk, ok := <-s.chunks:
		if !ok {
			return 0, false
		}
		s.cur = chunk[1:]
		return chunk[0], true
	case <-s.ctx.Done():
		return 0, false
	default:
	}
	timer := time.NewTimer(continuationTimeout)
	defer timer.Stop()
	select {
	case chunk, ok := <-s.chunks:
		if !ok {
			return 0, false
		}
		s.cur = chunk[1:]
		return chunk[0], true
	case <-timer.C:
		return 0, false
	case <-s.ctx.Done():
		return 0, false
	}
}
