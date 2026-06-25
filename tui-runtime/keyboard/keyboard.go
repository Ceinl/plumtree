package keyboard

import (
	"context"
	"io"
	"os"
	"time"
	"unicode/utf8"
)

type Event struct {
	Type   EventType
	Ch     rune
	Text   string
	Shift  bool
	Ctrl   bool
	Alt    bool
	Cmd    bool
	Mouse  bool
	MouseX int
	MouseY int
	Raw    []byte
}

const (
	byteEnter     = 13
	byteBackspace = 127
	byteCtrlC     = 3
	byteTab       = 9
	byteEscape    = 27
	byteBracket   = 91
	byteO         = 79
	byteTilde     = 126
	byteSemicolon = 59
	byteNum2      = 50
	byteNum5      = 53
)

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
	KeyPaste
	KeyUnknown
)

type byteReader struct {
	in <-chan byte
}

func (r *byteReader) readByte(ctx context.Context) (byte, bool) {
	select {
	case <-ctx.Done():
		return 0, false
	case b, ok := <-r.in:
		return b, ok
	}
}

func (r *byteReader) readByteTimeout(timeout time.Duration) (byte, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case b, ok := <-r.in:
		return b, ok
	case <-timer.C:
		return 0, false
	}
}

func Listen(ctx context.Context) <-chan Event {
	return ListenReader(ctx, os.Stdin)
}

// ListenReader parses keyboard input from an arbitrary reader (e.g. an SSH
// channel) rather than stdin, emitting Events until the reader ends or ctx is
// cancelled.
func ListenReader(ctx context.Context, r io.Reader) <-chan Event {
	bytes := readInputBytes(ctx, r)
	out := make(chan Event)
	go func() {
		defer close(out)
		reader := &byteReader{in: bytes}
		for {
			b, ok := reader.readByte(ctx)
			if !ok {
				return
			}

			ev := parseByte(reader, b)
			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}
		}
	}()
	return out
}

func readInputBytes(ctx context.Context, input io.Reader) <-chan byte {
	out := make(chan byte, 64)
	go func() {
		defer close(out)
		buf := make([]byte, 1)
		for {
			n, err := input.Read(buf)
			if err != nil || n == 0 {
				return
			}
			select {
			case <-ctx.Done():
				return
			case out <- buf[0]:
			}
		}
	}()
	return out
}

func parseByte(reader *byteReader, b byte) Event {
	if b == byteEscape {
		return readEscapeSequence(reader)
	}
	if b&0x80 != 0 {
		return readUTF8Sequence(reader, b)
	}
	return parseSingleByte(b)
}

func readUTF8Sequence(reader *byteReader, first byte) Event {
	n := utf8Bytes(first)
	if n == 1 {
		r, _ := utf8.DecodeRune([]byte{first})
		return Event{Type: KeyRune, Ch: r}
	}
	buf := make([]byte, n)
	buf[0] = first
	for i := 1; i < n; i++ {
		b, ok := reader.readByteTimeout(50 * time.Millisecond)
		if !ok {
			break
		}
		buf[i] = b
	}
	r, _ := utf8.DecodeRune(buf)
	return Event{Type: KeyRune, Ch: r}
}

func utf8Bytes(first byte) int {
	if first&0x80 == 0 {
		return 1
	}
	if first&0xE0 == 0xC0 {
		return 2
	}
	if first&0xF0 == 0xE0 {
		return 3
	}
	if first&0xF8 == 0xF0 {
		return 4
	}
	return 1
}

func parseSingleByte(b byte) Event {
	switch b {
	case byteEnter:
		return Event{Type: KeyEnter}
	case byteBackspace:
		return Event{Type: KeyBackspace}
	case byteCtrlC:
		return Event{Type: KeyCtrlC}
	case byteTab:
		return Event{Type: KeyTab}
	case 0:
		return Event{Type: KeyCtrlC}
	}

	if b < 0x20 {
		// Control character
		return Event{Type: KeyRune, Ch: rune(b + 0x40), Ctrl: true}
	}

	r, _ := utf8.DecodeRune([]byte{b})
	return Event{Type: KeyRune, Ch: r}
}
