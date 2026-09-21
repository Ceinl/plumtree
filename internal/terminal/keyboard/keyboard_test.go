package keyboard

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMouseReportsDoNotBecomeKeyEvents(t *testing.T) {
	input := "\x1b[<0;10;5M\x1b[<32;11;6M\x1b[<0;11;6m\x1b[<64;11;6M\x1b[<65;11;6M\x1b[<2;11;6Mq"
	var events []Event
	for ev := range ListenReader(context.Background(), strings.NewReader(input)) {
		events = append(events, ev)
	}
	want := []EventType{KeyMouseLeftDown, KeyMouseLeftDrag, KeyMouseLeftUp, KeyMouseWheelUp, KeyMouseWheelDown, KeyRune}
	if len(events) != len(want) {
		t.Fatalf("got %d events: %+v", len(events), events)
	}
	for i, typ := range want {
		if events[i].Type != typ {
			t.Fatalf("event %d: %+v", i, events[i])
		}
	}
	if events[0].MouseX != 9 || events[0].MouseY != 4 || events[5].Ch != 'q' {
		t.Fatalf("wrong coordinates or trailing key: %+v", events)
	}
}

func TestSplitMouseAndUnicodeInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, w := io.Pipe()
	defer r.Close()
	events := ListenReader(ctx, r)
	go func() {
		defer w.Close()
		for _, part := range []string{"\x1b[<", "0;", "10;5M", "λ", "\x1b"} {
			io.WriteString(w, part)
		}
	}()
	var got []Event
	for ev := range events {
		got = append(got, ev)
	}
	if len(got) != 3 || got[0].Type != KeyMouseLeftDown || got[1].Ch != 'λ' || got[2].Type != KeyEscape {
		t.Fatalf("events=%+v", got)
	}
}

func TestUnsupportedReportsAreConsumed(t *testing.T) {
	for _, report := range []string{"\x1b[?1;2c", "\x1b[<0;0;1M", "\x1b[<0;;1M", "\x1b[" + strings.Repeat("1", 64) + "q", "\x1b[" + strings.Repeat("1", 128) + "q"} {
		var got []Event
		for ev := range ListenReader(context.Background(), strings.NewReader(report+"q")) {
			got = append(got, ev)
		}
		if len(got) != 1 || got[0].Ch != 'q' {
			t.Fatalf("%q leaked into keys: %+v", report, got)
		}
	}
}

func TestInvalidUnicodePreservesFollowingInput(t *testing.T) {
	for _, input := range []string{"\xe2q", "\xe2\x82q", "\xffq"} {
		for _, split := range []bool{false, true} {
			var reader io.Reader = strings.NewReader(input)
			if split {
				var parts []io.Reader
				for i := range input {
					parts = append(parts, strings.NewReader(input[i:i+1]))
				}
				reader = io.MultiReader(parts...)
			}
			var got []rune
			for event := range ListenReader(context.Background(), reader) {
				got = append(got, event.Ch)
			}
			want := strings.Repeat(string(utf8.RuneError), len(input)-1) + "q"
			if string(got) != want {
				t.Fatalf("input %x split=%v: got %q, want %q", input, split, string(got), want)
			}
		}
	}
}
