package keyboard

import (
	"context"
	"io"
	"os"
	"time"
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
)

func Listen(ctx context.Context) <-chan Event { return ListenReader(ctx, os.Stdin) }

func ListenReader(ctx context.Context, input io.Reader) <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		bytes := make(chan byte, 64)
		go func() {
			defer close(bytes)
			buf := []byte{0}
			for {
				n, err := input.Read(buf)
				if err != nil || n == 0 {
					return
				}
				select {
				case bytes <- buf[0]:
				case <-ctx.Done():
					return
				}
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case b, ok := <-bytes:
				if !ok {
					return
				}
				event := parseByte(ctx, bytes, b)
				if event.Type == KeyEscape && event.Ch == 0 {
					continue
				}
				select {
				case out <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func parseByte(ctx context.Context, input <-chan byte, b byte) Event {
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
		read := func() (byte, bool) {
			select {
			case v, ok := <-input:
				return v, ok
			case <-time.After(10 * time.Millisecond):
				return 0, false
			case <-ctx.Done():
				return 0, false
			}
		}
		b1, ok := read()
		if !ok {
			return Event{Type: KeyEscape}
		}
		if b1 != '[' {
			return Event{Type: KeyEscape}
		}
		b2, ok := read()
		if !ok {
			return Event{Type: KeyEscape}
		}
		switch b2 {
		case 'A':
			return Event{Type: KeyArrowUp}
		case 'B':
			return Event{Type: KeyArrowDown}
		case 'C':
			return Event{Type: KeyArrowRight}
		case 'D':
			return Event{Type: KeyArrowLeft}
		case 'H':
			return Event{Type: KeyHome}
		case 'F':
			return Event{Type: KeyEnd}
		case '3':
			if b3, ok := read(); ok && b3 == '~' {
				return Event{Type: KeyDelete}
			}
		case '5':
			if b3, ok := read(); ok && b3 == '~' {
				return Event{Type: KeyPageUp}
			}
		case '6':
			if b3, ok := read(); ok && b3 == '~' {
				return Event{Type: KeyPageDown}
			}
		}
		return Event{Type: KeyEscape}
	default:
		return Event{Type: KeyRune, Ch: rune(b)}
	}
}
