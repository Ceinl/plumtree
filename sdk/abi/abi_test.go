package abi

import (
	"reflect"
	"testing"
)

func TestEventRoundTrip(t *testing.T) {
	cases := []Event{
		{Kind: KindNone},
		{Kind: KindKey, Key: KeyArrowUp},
		{Kind: KindKey, Key: KeyRune, Ch: 'q'},
		{Kind: KindKey, Key: KeyRune, Ch: '世', Mods: ModCtrl | ModShift},
		{Kind: KindKey, Key: KeyCtrlC, Mods: ModCtrl},
		{Kind: KindResize, W: 120, H: 40},
		{Kind: KindMessage, Topic: "room", Data: []byte("hello 世界")},
		{Kind: KindMessage, Topic: "", Data: nil},
	}
	for _, want := range cases {
		got, err := DecodeEvent(EncodeEvent(want))
		if err != nil {
			t.Fatalf("DecodeEvent(%+v): %v", want, err)
		}
		// Normalize nil vs empty payload: the wire carries length 0 either way.
		if want.Kind == KindMessage && len(want.Data) == 0 && len(got.Data) == 0 {
			got.Data, want.Data = nil, nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("round trip: got %+v want %+v", got, want)
		}
	}
}

func TestFrameRoundTrip(t *testing.T) {
	want := Frame{
		W: 2, H: 2, Quit: true,
		Cells: []Cell{
			{Ch: 'a', Fg: RGB{1, 2, 3}, Bg: RGB{4, 5, 6}, Decor: DecorBold},
			{Ch: '世', Fg: RGB{255, 0, 0}, Bg: RGB{0, 0, 0}},
			{Ch: ' ', Fg: RGB{10, 20, 30}, Bg: RGB{40, 50, 60}, Decor: DecorItalic | DecorUnderline},
			{Ch: 'z'},
		},
	}
	got, err := DecodeFrame(EncodeFrame(want))
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, want)
	}
	if c := got.At(0, 1); c.Ch != ' ' { // index y*W+x = 2 -> the space cell
		t.Errorf("At(0,1).Ch = %q, want space", c.Ch)
	}
	if c := got.At(1, 1); c.Ch != 'z' {
		t.Errorf("At(1,1).Ch = %q, want z", c.Ch)
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	for _, want := range []Identity{
		{User: "SHA256:abcdef", Authenticated: true},
		{User: "anon-0011", Authenticated: false},
		{User: "", Authenticated: false},
	} {
		got, err := DecodeIdentity(EncodeIdentity(want))
		if err != nil {
			t.Fatalf("DecodeIdentity(%+v): %v", want, err)
		}
		if got != want {
			t.Errorf("round trip: got %+v want %+v", got, want)
		}
	}
}

func TestDecodeRejectsBadInput(t *testing.T) {
	if _, err := DecodeEvent([]byte{0x00}); err == nil {
		t.Error("short event should error")
	}
	if _, err := DecodeFrame([]byte{0xFF, 0, 0, 0, 0, 0, 0, 0}); err != ErrMagic {
		t.Errorf("bad magic: got %v want ErrMagic", err)
	}
	// Header claims 4x4 but no cell bytes follow.
	bad := EncodeFrame(Frame{})
	bad[0] = magicFrame
	bad[4], bad[6] = 4, 4
	if _, err := DecodeFrame(bad); err != ErrSize {
		t.Errorf("oversize: got %v want ErrSize", err)
	}
}

func TestParseSGRColor(t *testing.T) {
	if c, ok := ParseSGRColor("\x1b[38;2;200;200;200m"); !ok || c != (RGB{200, 200, 200}) {
		t.Errorf("fg parse = %+v ok=%v", c, ok)
	}
	if c, ok := ParseSGRColor("\x1b[48;2;25;23;29m"); !ok || c != (RGB{25, 23, 29}) {
		t.Errorf("bg parse = %+v ok=%v", c, ok)
	}
	// Hostile / malformed inputs must be rejected, not parsed.
	for _, bad := range []string{
		"", "plain text", "\x1b[2J", "\x1b[31m",
		"\x1b[38;5;200m", "\x1b[38;2;999;0;0m", "\x1b]0;title\x07",
	} {
		if _, ok := ParseSGRColor(bad); ok {
			t.Errorf("ParseSGRColor(%q) should be rejected", bad)
		}
	}
}

func TestParseSGRDecor(t *testing.T) {
	if d := ParseSGRDecor("\x1b[1;3;4m"); d != (DecorBold | DecorItalic | DecorUnderline) {
		t.Errorf("decor = %08b", d)
	}
	if d := ParseSGRDecor(""); d != 0 {
		t.Errorf("empty decor = %08b, want 0", d)
	}
	if d := ParseSGRDecor("\x1b[1m"); d != DecorBold {
		t.Errorf("bold = %08b", d)
	}
}

func TestSGRRoundTripThroughHostRenderers(t *testing.T) {
	// A color encoded by the runtime-style string, parsed to RGB, then
	// re-rendered host-side, should reproduce the same RGB.
	orig := RGB{12, 200, 7}
	c, ok := ParseSGRColor(FgSGR(orig))
	if !ok || c != orig {
		t.Errorf("fg sgr round trip: %+v ok=%v", c, ok)
	}
	if got := DecorSGR(DecorBold | DecorUnderline); ParseSGRDecor(got) != (DecorBold | DecorUnderline) {
		t.Errorf("decor sgr round trip failed: %q", got)
	}
}

func TestKVResultCodes(t *testing.T) {
	// Success is non-negative; every error is a distinct negative value, so a
	// kv_get caller can treat any >=0 return as a value length and only the
	// negatives as errors.
	if KVOk != 0 {
		t.Errorf("KVOk = %d, want 0", KVOk)
	}
	errs := map[string]int32{
		"NotFound": KVErrNotFound,
		"TooLarge": KVErrTooLarge,
		"Quota":    KVErrQuota,
		"Internal": KVErrInternal,
	}
	seen := map[int32]string{}
	for name, code := range errs {
		if code >= 0 {
			t.Errorf("%s = %d, want negative", name, code)
		}
		if other, dup := seen[code]; dup {
			t.Errorf("%s and %s share code %d", name, other, code)
		}
		seen[code] = name
	}
	if KVMaxKey <= 0 || KVMaxValue <= 0 {
		t.Errorf("size caps must be positive: key=%d value=%d", KVMaxKey, KVMaxValue)
	}
}
