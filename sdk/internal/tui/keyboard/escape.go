package keyboard

import "time"

func readEscapeSequence(reader *byteReader) Event {
	next, ok := reader.readByteTimeout(50 * time.Millisecond)
	if !ok {
		return Event{Type: KeyEscape}
	}

	if next == byteBracket {
		return readCSI(reader)
	}

	if next == byteEscape {
		ev := readEscapeSequence(reader)
		ev.Alt = true
		return ev
	}

	if next == byteO {
		next2, ok2 := reader.readByteTimeout(50 * time.Millisecond)
		if !ok2 {
			return Event{Type: KeyUnknown}
		}
		return parseSS3(next2)
	}

	if next < 0x80 {
		if next == byteEnter {
			return Event{Type: KeyEnter, Alt: true}
		}
		ev := parseSingleByte(next)
		ev.Alt = true
		return ev
	}

	if next&0x80 != 0 {
		ev := readUTF8Sequence(reader, next)
		ev.Alt = true
		return ev
	}

	return Event{Type: KeyUnknown}
}

func readCSI(reader *byteReader) Event {
	params := make([]byte, 0, 8)
	final := byte(0)

	for {
		b, ok := reader.readByteTimeout(50 * time.Millisecond)
		if !ok {
			break
		}
		if b >= 0x40 && b <= 0x7E {
			final = b
			break
		}
		params = append(params, b)
	}

	if final == 'M' && len(params) == 0 {
		return readX10Mouse(reader)
	}
	return parseCSI(reader, params, final)
}

func parseCSI(reader *byteReader, params []byte, final byte) Event {
	if len(params) > 0 && params[0] == '<' {
		return parseSGRMouse(params[1:], final)
	}

	paramNums := parseParams(params)
	if (final == 'M' || final == 'm') && len(paramNums) >= 3 {
		return parseMouseButton(paramNums[0], paramNums[1]-1, paramNums[2]-1, final == 'M')
	}

	modifier := 1
	if len(paramNums) >= 2 {
		modifier = paramNums[len(paramNums)-1]
	}
	if len(paramNums) == 0 {
		paramNums = append(paramNums, 1)
	}

	shift, ctrl, alt, cmd := modifierFlags(modifier)
	if final == '~' && len(paramNums) >= 3 && paramNums[0] == byteEscape {
		shift, ctrl, alt, cmd = modifierFlags(paramNums[1])
		return modifiedCodepointEvent(paramNums[2], shift, ctrl, alt, cmd)
	}

	switch final {
	case 'A':
		return Event{Type: KeyArrowUp, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 'B':
		return Event{Type: KeyArrowDown, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 'C':
		return Event{Type: KeyArrowRight, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 'D':
		return Event{Type: KeyArrowLeft, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 'H':
		return Event{Type: KeyHome, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 'F':
		return Event{Type: KeyEnd, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 'Z':
		return Event{Type: KeyTab, Shift: true, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 'u':
		return modifiedCodepointEvent(paramNums[0], shift, ctrl, alt, cmd)
	case '~':
		return parseTildeCSI(reader, paramNums, shift, ctrl, alt, cmd)
	}

	return Event{Type: KeyUnknown}
}

func parseParams(params []byte) []int {
	var nums []int
	current := 0
	for _, b := range params {
		if b == byteSemicolon {
			nums = append(nums, current)
			current = 0
			continue
		}
		if b >= '0' && b <= '9' {
			current = current*10 + int(b-'0')
		}
	}
	return append(nums, current)
}

func parseTildeCSI(reader *byteReader, params []int, shift, ctrl, alt, cmd bool) Event {
	switch params[0] {
	case 1:
		return Event{Type: KeyHome, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 3:
		return Event{Type: KeyDelete, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 4:
		return Event{Type: KeyEnd, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 5:
		return Event{Type: KeyPageUp, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 6:
		return Event{Type: KeyPageDown, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case 200:
		return readBracketedPaste(reader)
	case 201:
		return Event{Type: KeyUnknown}
	}
	return Event{Type: KeyUnknown}
}

func modifierFlags(modifier int) (shift, ctrl, alt, cmd bool) {
	bits := modifier - 1
	if bits < 0 {
		bits = 0
	}
	shift = bits&1 != 0
	alt = bits&2 != 0
	ctrl = bits&4 != 0
	cmd = bits&8 != 0
	return shift, ctrl, alt, cmd
}

func modifiedCodepointEvent(codepoint int, shift, ctrl, alt, cmd bool) Event {
	switch codepoint {
	case byteTab:
		return Event{Type: KeyTab, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case byteEnter:
		return Event{Type: KeyEnter, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	case byteEscape:
		return Event{Type: KeyEscape, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
	}

	ch := rune(codepoint)
	if (ctrl || cmd) && (ch == 'c' || ch == 'C') {
		return Event{Type: KeyCtrlC, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd, Ch: ch}
	}
	return Event{Type: KeyRune, Ch: ch, Shift: shift, Ctrl: ctrl, Alt: alt, Cmd: cmd}
}

func parseSS3(b byte) Event {
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
	return Event{Type: KeyUnknown}
}
