package runner

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestParseToken(t *testing.T) {
	cases := map[string]abi.Event{
		"up":     {Kind: abi.KindKey, Key: abi.KeyArrowUp},
		"down":   {Kind: abi.KindKey, Key: abi.KeyArrowDown},
		"enter":  {Kind: abi.KindKey, Key: abi.KeyEnter},
		"ctrl-c": {Kind: abi.KindKey, Key: abi.KeyCtrlC, Mods: abi.ModCtrl},
		"q":      {Kind: abi.KindKey, Key: abi.KeyRune, Ch: 'q'},
		"none":   {Kind: abi.KindNone},
	}
	for tok, want := range cases {
		got, ok := ParseToken(tok)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("ParseToken(%q) = %+v,%v want %+v", tok, got, ok, want)
		}
	}
	if _, ok := ParseToken("notakey"); ok {
		t.Error("multi-rune unknown token should not parse")
	}
}

func TestSanitizeRuneStripsControls(t *testing.T) {
	for _, r := range []rune{0, 0x07, 0x1b, '\n', 0x7f, 0x9b} {
		if sanitizeRune(r) != ' ' {
			t.Errorf("sanitizeRune(%#x) should be space", r)
		}
	}
	for _, r := range []rune{'a', '世', '↑'} {
		if sanitizeRune(r) != r {
			t.Errorf("sanitizeRune(%q) changed", r)
		}
	}
}

func TestControlFilterStripsEscapes(t *testing.T) {
	var buf bytes.Buffer
	f := &controlFilter{w: &buf}
	n, err := f.Write([]byte("ok\x1b[31mred\x1b[0m\there\x07\n"))
	if err != nil {
		t.Fatal(err)
	}
	if n != len("ok\x1b[31mred\x1b[0m\there\x07\n") {
		t.Errorf("Write reported %d bytes", n)
	}
	got := buf.String()
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x07) {
		t.Errorf("control bytes leaked: %q", got)
	}
	if !strings.Contains(got, "\t") || !strings.HasSuffix(got, "\n") {
		t.Errorf("tab/newline should be preserved: %q", got)
	}
}

func TestControlFilterDropsC1AndKeepsUTF8(t *testing.T) {
	var buf bytes.Buffer
	f := &controlFilter{w: &buf}
	// 0x9b (CSI) and 0x9d (OSC) are 8-bit C1 introducers a byte-wise C0 filter
	// would pass; "café 世 🌳" exercises 2-, 3-, and 4-byte UTF-8 that must survive.
	in := "café \x9b31m 世 \x9d0;x\x07 🌳"
	if _, err := f.Write([]byte(in)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	// Scan at the rune level: a standalone C1 introducer decodes to a lone
	// 0x80–0x9f rune, whereas the same byte values appearing as UTF-8
	// continuation bytes inside 世/🌳 decode as part of their multibyte rune.
	for _, r := range got {
		if r >= 0x80 && r <= 0x9f {
			t.Errorf("C1 rune 0x%02x leaked: %q", r, got)
		}
	}
	for _, want := range []string{"café", "世", "🌳"} {
		if !strings.Contains(got, want) {
			t.Errorf("multibyte %q was corrupted: %q", want, got)
		}
	}
}

func TestControlFilterCarriesSplitRune(t *testing.T) {
	var buf bytes.Buffer
	f := &controlFilter{w: &buf}
	tree := []byte("🌳") // 4 bytes: split across two writes
	if _, err := f.Write(tree[:2]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(tree[2:]); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "🌳" {
		t.Errorf("split rune not reassembled: %q", got)
	}
}

func TestTextSinkRendersGrid(t *testing.T) {
	f := abi.Frame{W: 3, H: 2, Cells: []abi.Cell{
		{Ch: 'a'}, {Ch: 'b'}, {Ch: 'c'},
		{Ch: 0x1b}, {Ch: 'x'}, {Ch: 'y'}, // escape rune must be sanitized
	}}
	var buf bytes.Buffer
	TextSink{W: &buf}.Present(f)
	out := buf.String()
	if !strings.Contains(out, "│abc│") {
		t.Errorf("missing row: %q", out)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("escape leaked into text render: %q", out)
	}
}

func TestTTYSinkUsesDefaultsForZeroColors(t *testing.T) {
	var buf bytes.Buffer
	sink := NewTTYSinkWriter(1, 1, 0, &buf)
	sink.Present(abi.Frame{W: 1, H: 1, Cells: []abi.Cell{{Ch: 'X'}}})
	out := buf.String()
	if !strings.Contains(out, "\x1b[38;2;200;200;200m") {
		t.Fatalf("default foreground missing from output: %q", out)
	}
	if strings.Contains(out, "\x1b[38;2;0;0;0m") || strings.Contains(out, "\x1b[48;2;0;0;0m") {
		t.Fatalf("zero RGB rendered as black: %q", out)
	}
}

func TestWatchdogFiresAfterTimeout(t *testing.T) {
	var fired atomic.Bool
	wd := &watchdog{timeout: 30 * time.Millisecond, cancel: func() { fired.Store(true) }}
	wd.arm()
	time.Sleep(80 * time.Millisecond)
	if !fired.Load() || !wd.fired.Load() {
		t.Error("watchdog should have fired and cancelled")
	}
}

func TestWatchdogDisarmPreventsFire(t *testing.T) {
	var fired atomic.Bool
	wd := &watchdog{timeout: 50 * time.Millisecond, cancel: func() { fired.Store(true) }}
	wd.arm()
	time.Sleep(10 * time.Millisecond)
	wd.disarm()
	time.Sleep(80 * time.Millisecond)
	if fired.Load() {
		t.Error("disarmed watchdog should not fire")
	}
}

// ScriptSource hands out a resize first, then the tokens, then stops.
func TestScriptSourceSequence(t *testing.T) {
	src := NewScriptSource(20, 5, []string{"up", "q"})
	first, ok := src.Next(context.Background())
	if !ok || first.Kind != abi.KindResize || first.W != 20 || first.H != 5 {
		t.Fatalf("first event = %+v,%v want resize 20x5", first, ok)
	}
	if ev, ok := src.Next(context.Background()); !ok || ev.Key != abi.KeyArrowUp {
		t.Errorf("second event = %+v,%v want up", ev, ok)
	}
	if ev, ok := src.Next(context.Background()); !ok || ev.Ch != 'q' {
		t.Errorf("third event = %+v,%v want q", ev, ok)
	}
	if _, ok := src.Next(context.Background()); ok {
		t.Error("source should be exhausted")
	}
}
