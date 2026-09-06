package keyboard

import "time"

// continuationTimeout bounds how long a sequence split across reads may keep
// the parser waiting for its next byte.
const continuationTimeout = 10 * time.Millisecond

// Consume the whole CSI sequence, including unsupported reports. Its parameter
// bytes must never become ordinary keys and trigger one redraw per byte.
func readCSI(read func() (byte, bool)) Event {
	var params [64]byte
	for n := 0; ; {
		b, ok := read()
		if !ok {
			return Event{Type: KeyUnknown}
		}
		if b >= 0x40 && b <= 0x7e {
			if n == len(params) {
				return Event{Type: KeyUnknown}
			}
			if n > 0 && params[0] == '<' {
				return parseMouse(params[1:n], b)
			}
			if n == 0 {
				switch b {
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
				}
			}
			if n == 1 && b == '~' {
				switch params[0] {
				case '3':
					return Event{Type: KeyDelete}
				case '5':
					return Event{Type: KeyPageUp}
				case '6':
					return Event{Type: KeyPageDown}
				}
			}
			return Event{Type: KeyUnknown}
		}
		if n < len(params) {
			params[n] = b
			n++
		}
	}
}

// SGR mouse reports are the format enabled by the SSH terminal setup.
func parseMouse(params []byte, final byte) Event {
	unknown := Event{Type: KeyUnknown}
	if final != 'M' && final != 'm' {
		return unknown
	}
	var values [3]int
	index, digits := 0, 0
	for _, b := range params {
		if b == ';' && digits > 0 && index < 2 {
			index++
			digits = 0
			continue
		}
		if b < '0' || b > '9' || digits >= 6 {
			return unknown
		}
		values[index] = values[index]*10 + int(b-'0')
		digits++
	}
	if index != 2 || digits == 0 || values[1] < 1 || values[2] < 1 {
		return unknown
	}
	button := values[0]
	ev := Event{Mouse: true, MouseX: values[1] - 1, MouseY: values[2] - 1, Shift: button&4 != 0, Alt: button&8 != 0, Ctrl: button&16 != 0}
	switch {
	case button&64 != 0:
		if final != 'M' {
			return unknown
		}
		switch button & 3 {
		case 0:
			ev.Type = KeyMouseWheelUp
		case 1:
			ev.Type = KeyMouseWheelDown
		default:
			return unknown
		}
	case button&3 != 0:
		return unknown
	case final == 'm':
		ev.Type = KeyMouseLeftUp
	case button&32 != 0:
		ev.Type = KeyMouseLeftDrag
	default:
		ev.Type = KeyMouseLeftDown
	}
	return ev
}
