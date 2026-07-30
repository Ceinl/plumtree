package keyboard

import (
	"context"
	"strings"
	"testing"
)

func parseEvents(input []byte) []Event {
	in := make(chan byte, len(input))
	for _, b := range input {
		in <- b
	}
	close(in)

	reader := &byteReader{in: in}
	events := []Event{}
	for {
		b, ok := reader.readByte(context.Background())
		if !ok {
			return events
		}
		events = append(events, parseByte(reader, b))
	}
}

func requireEvent(t *testing.T, events []Event) Event {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %#v", len(events), events)
	}
	return events[0]
}

func TestParseBareEscape(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte{byteEscape}))
	if ev.Type != KeyEscape {
		t.Fatalf("expected escape, got %#v", ev)
	}
}

func TestParseAltEnter(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte{byteEscape, byteEnter}))
	if ev.Type != KeyEnter || !ev.Alt {
		t.Fatalf("expected Alt+Enter, got %#v", ev)
	}
}

func TestParseShiftEnterCSIU(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[13;2u")))
	if ev.Type != KeyEnter || !ev.Shift || ev.Alt || ev.Ctrl {
		t.Fatalf("expected Shift+Enter, got %#v", ev)
	}
}

func TestParseTabCSIU(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[9u")))
	if ev.Type != KeyTab || ev.Shift || ev.Alt || ev.Ctrl {
		t.Fatalf("expected Tab, got %#v", ev)
	}
}

func TestParseShiftTabCSIU(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[9;2u")))
	if ev.Type != KeyTab || !ev.Shift || ev.Alt || ev.Ctrl {
		t.Fatalf("expected Shift+Tab, got %#v", ev)
	}
}

func TestParseShiftTabCSI(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[Z")))
	if ev.Type != KeyTab || !ev.Shift || ev.Alt || ev.Ctrl {
		t.Fatalf("expected Shift+Tab, got %#v", ev)
	}
}

func TestParseShiftEnterModifyOtherKeys(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[27;2;13~")))
	if ev.Type != KeyEnter || !ev.Shift || ev.Alt || ev.Ctrl {
		t.Fatalf("expected Shift+Enter, got %#v", ev)
	}
}

func TestParseShiftTabModifyOtherKeys(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[27;2;9~")))
	if ev.Type != KeyTab || !ev.Shift || ev.Alt || ev.Ctrl {
		t.Fatalf("expected Shift+Tab, got %#v", ev)
	}
}

func TestParseCtrlCCSIU(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[99;5u")))
	if ev.Type != KeyCtrlC || !ev.Ctrl || ev.Ch != 'c' {
		t.Fatalf("expected Ctrl+C, got %#v", ev)
	}
}

func TestParseCtrlSCSIU(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[115;5u")))
	if ev.Type != KeyRune || !ev.Ctrl || ev.Ch != 's' {
		t.Fatalf("expected Ctrl+S, got %#v", ev)
	}
}

func TestParseCmdZCSIU(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[122;9u")))
	if ev.Type != KeyRune || !ev.Cmd || ev.Ctrl || ev.Alt || ev.Ch != 'z' {
		t.Fatalf("expected Cmd+Z, got %#v", ev)
	}
}

func TestParseCmdCCSIU(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[99;9u")))
	if ev.Type != KeyCtrlC || !ev.Cmd || ev.Ctrl || ev.Ch != 'c' {
		t.Fatalf("expected Cmd+C, got %#v", ev)
	}
}

func TestParseCtrlSModifyOtherKeys(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[27;5;115~")))
	if ev.Type != KeyRune || !ev.Ctrl || ev.Ch != 's' {
		t.Fatalf("expected Ctrl+S, got %#v", ev)
	}
}

func TestParseArrowWithModifiers(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[1;5D")))
	if ev.Type != KeyArrowLeft || !ev.Ctrl || ev.Shift || ev.Alt {
		t.Fatalf("expected Ctrl+Left, got %#v", ev)
	}
}

func TestParseUTF8Rune(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("é")))
	if ev.Type != KeyRune || ev.Ch != 'é' {
		t.Fatalf("expected UTF-8 rune, got %#v", ev)
	}
}

func TestParseMouseWheel(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[<64;10;5M")))
	if ev.Type != KeyMouseWheelUp || !ev.Mouse || ev.MouseX != 9 || ev.MouseY != 4 {
		t.Fatalf("expected mouse wheel up at 9,4, got %#v", ev)
	}
}

func TestParseMouseWheelIgnoresHorizontalScroll(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[<66;10;5M")))
	if ev.Type != KeyUnknown {
		t.Fatalf("expected horizontal wheel ignored, got %#v", ev)
	}
}

func TestParseMouseWheelIgnoresReleaseEvents(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[<64;10;5m")))
	if ev.Type != KeyUnknown {
		t.Fatalf("expected wheel release ignored, got %#v", ev)
	}
}

func TestParseMouseLeftDown(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[<0;10;5M")))
	if ev.Type != KeyMouseLeftDown || !ev.Mouse || ev.MouseX != 9 || ev.MouseY != 4 {
		t.Fatalf("expected mouse left down at 9,4, got %#v", ev)
	}
}

func TestParseX10MouseLeftDown(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte{byteEscape, '[', 'M', 32, 42, 37}))
	if ev.Type != KeyMouseLeftDown || !ev.Mouse || ev.MouseX != 9 || ev.MouseY != 4 {
		t.Fatalf("expected X10 mouse left down at 9,4, got %#v", ev)
	}
}

func TestParseURXVTMouseLeftDown(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[0;10;5M")))
	if ev.Type != KeyMouseLeftDown || !ev.Mouse || ev.MouseX != 9 || ev.MouseY != 4 {
		t.Fatalf("expected URXVT mouse left down at 9,4, got %#v", ev)
	}
}

func TestParseMouseLeftDrag(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[<32;15;8M")))
	if ev.Type != KeyMouseLeftDrag || !ev.Mouse || ev.MouseX != 14 || ev.MouseY != 7 {
		t.Fatalf("expected mouse left drag at 14,7, got %#v", ev)
	}
}

func TestParseMouseLeftUp(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[<0;10;5m")))
	if ev.Type != KeyMouseLeftUp || !ev.Mouse || ev.MouseX != 9 || ev.MouseY != 4 {
		t.Fatalf("expected mouse left up at 9,4, got %#v", ev)
	}
}

func TestParseBracketedPaste(t *testing.T) {
	ev := requireEvent(t, parseEvents([]byte("\x1b[200~one\ntwo\x1b[201~")))
	if ev.Type != KeyPaste || ev.Text != "one\ntwo" {
		t.Fatalf("expected bracketed paste, got %#v", ev)
	}
}

func TestReadInputBytesClosesOnEOF(t *testing.T) {
	bytes := readInputBytes(context.Background(), strings.NewReader(""))
	if b, ok := <-bytes; ok {
		t.Fatalf("expected closed input channel, got byte %q", b)
	}
}
