package keyboard

import "time"

func readX10Mouse(reader *byteReader) Event {
	button, ok := reader.readByteTimeout(50 * time.Millisecond)
	if !ok {
		return Event{Type: KeyUnknown}
	}
	xb, ok := reader.readByteTimeout(50 * time.Millisecond)
	if !ok {
		return Event{Type: KeyUnknown}
	}
	yb, ok := reader.readByteTimeout(50 * time.Millisecond)
	if !ok {
		return Event{Type: KeyUnknown}
	}

	return parseMouseButton(int(button)-32, int(xb)-33, int(yb)-33, true)
}

func parseSGRMouse(params []byte, final byte) Event {
	nums := parseParams(params)
	if len(nums) < 3 {
		return Event{Type: KeyUnknown}
	}
	return parseMouseButton(nums[0], nums[1]-1, nums[2]-1, final == 'M')
}

func parseMouseButton(button, x, y int, isPress bool) Event {
	if isPress && button&64 != 0 {
		scrollButton := button & 3
		if scrollButton == 0 {
			return Event{Type: KeyMouseWheelUp, Mouse: true, MouseX: x, MouseY: y}
		}
		if scrollButton == 1 {
			return Event{Type: KeyMouseWheelDown, Mouse: true, MouseX: x, MouseY: y}
		}
		return Event{Type: KeyUnknown}
	}

	if button == 3 || button == 35 {
		return Event{Type: KeyUnknown}
	}
	if !isPress && button&64 != 0 {
		return Event{Type: KeyUnknown}
	}

	isDrag := button&32 != 0
	baseButton := button & 3
	if isDrag {
		if baseButton == 0 {
			return Event{Type: KeyMouseLeftDrag, Mouse: true, MouseX: x, MouseY: y}
		}
		return Event{Type: KeyUnknown}
	}

	if isPress {
		if baseButton == 0 {
			return Event{Type: KeyMouseLeftDown, Mouse: true, MouseX: x, MouseY: y}
		}
		return Event{Type: KeyUnknown}
	}

	return Event{Type: KeyMouseLeftUp, Mouse: true, MouseX: x, MouseY: y}
}
