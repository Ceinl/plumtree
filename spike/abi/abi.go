// Package abi defines the versioned wire format exchanged between a Plumtree
// guest app (WASM) and the host runner across guest linear memory.
//
// Two messages exist in v0:
//
//   - Event  (host -> guest): an input event (a key, or "none" for a repaint).
//   - Frame  (guest -> host): a structured cell grid for the host to render.
//
// Design rules this format enforces:
//
//   - The guest never emits raw ANSI. Colors travel as structured RGB and text
//     decoration as a bitmask; the host alone turns them into terminal output.
//   - Key events use an ABI-stable KeyType enum independent of the host's
//     keyboard package, so the guest needn't import (or track) it.
//
// The encoding is a compact little-endian binary layout — deliberately not
// JSON, both to keep guest binaries lean and to model the real ptr/len ABI.
// It is versioned (Version) separately from any Go package version.
package abi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is the ABI wire version. Bump on any incompatible layout change.
const Version uint8 = 0

const (
	magicEvent byte = 0x01
	magicFrame byte = 0x02
)

// Kind discriminates Event payloads.
type Kind uint8

const (
	// KindNone is a repaint/tick with no input (e.g. resize or first frame).
	KindNone Kind = 0
	// KindKey is a keyboard event.
	KindKey Kind = 1
)

// KeyType is the ABI-stable key classification. It intentionally mirrors the
// subset of host key events a guest needs, decoupled from the host enum values.
type KeyType uint8

const (
	KeyRune KeyType = iota
	KeyEnter
	KeyBackspace
	KeyTab
	KeyEscape
	KeyCtrlC
	KeyArrowUp
	KeyArrowDown
	KeyArrowLeft
	KeyArrowRight
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyDelete
)

// Mods is a bitmask of active modifier keys.
type Mods uint8

const (
	ModShift Mods = 1 << iota
	ModCtrl
	ModAlt
	ModCmd
)

// Decoration bits, mirroring layout text decorations.
const (
	DecorBold      uint8 = 1 << 0
	DecorItalic    uint8 = 1 << 1
	DecorUnderline uint8 = 1 << 2
)

// RGB is a truecolor value.
type RGB struct{ R, G, B uint8 }

// Event is an input event sent host -> guest.
type Event struct {
	Kind Kind
	Key  KeyType
	Ch   rune
	Mods Mods
}

// Cell is one rendered character: a rune plus structured styling. No ANSI.
type Cell struct {
	Ch    rune
	Fg    RGB
	Bg    RGB
	Decor uint8
}

// Frame is a full structured render: a W*H row-major grid plus control flags.
type Frame struct {
	W, H  int
	Quit  bool
	Cells []Cell // len == W*H, row-major
}

// At returns the cell at (x,y); callers must keep coordinates in range.
func (f Frame) At(x, y int) Cell { return f.Cells[y*f.W+x] }

var (
	ErrShort   = errors.New("abi: buffer too short")
	ErrMagic   = errors.New("abi: bad magic byte")
	ErrVersion = errors.New("abi: unsupported version")
	ErrSize    = errors.New("abi: frame dimensions exceed buffer")
)

// --- Event ---------------------------------------------------------------

// EncodeEvent serializes an input event into 9 bytes. Layout (little-endian):
//
//	[0] magic=0x01  [1] version  [2] kind  [3] keyType
//	[4:8] rune int32  [8] mods
func EncodeEvent(e Event) []byte {
	b := make([]byte, 9)
	b[0] = magicEvent
	b[1] = Version
	b[2] = byte(e.Kind)
	b[3] = byte(e.Key)
	binary.LittleEndian.PutUint32(b[4:8], uint32(e.Ch))
	b[8] = byte(e.Mods)
	return b
}

// DecodeEvent parses bytes produced by EncodeEvent.
func DecodeEvent(b []byte) (Event, error) {
	if len(b) < 9 {
		return Event{}, ErrShort
	}
	if b[0] != magicEvent {
		return Event{}, ErrMagic
	}
	if b[1] != Version {
		return Event{}, ErrVersion
	}
	return Event{
		Kind: Kind(b[2]),
		Key:  KeyType(b[3]),
		Ch:   rune(binary.LittleEndian.Uint32(b[4:8])),
		Mods: Mods(b[8]),
	}, nil
}

// --- Frame ---------------------------------------------------------------

const frameHeaderLen = 8 // magic, version, flags, reserved, w(2), h(2)
const cellLen = 11       // rune(4) + fg(3) + bg(3) + decor(1)

const flagQuit byte = 1 << 0

// EncodeFrame serializes a structured frame. Header (little-endian):
//
//	[0] magic=0x02  [1] version  [2] flags  [3] reserved
//	[4:6] w uint16  [6:8] h uint16
//
// followed by W*H cells, each: rune int32 | fg RGB | bg RGB | decor uint8.
func EncodeFrame(f Frame) []byte {
	b := make([]byte, frameHeaderLen+len(f.Cells)*cellLen)
	b[0] = magicFrame
	b[1] = Version
	if f.Quit {
		b[2] |= flagQuit
	}
	binary.LittleEndian.PutUint16(b[4:6], uint16(f.W))
	binary.LittleEndian.PutUint16(b[6:8], uint16(f.H))

	off := frameHeaderLen
	for _, c := range f.Cells {
		binary.LittleEndian.PutUint32(b[off:off+4], uint32(c.Ch))
		b[off+4], b[off+5], b[off+6] = c.Fg.R, c.Fg.G, c.Fg.B
		b[off+7], b[off+8], b[off+9] = c.Bg.R, c.Bg.G, c.Bg.B
		b[off+10] = c.Decor
		off += cellLen
	}
	return b
}

// DecodeFrame parses bytes produced by EncodeFrame.
func DecodeFrame(b []byte) (Frame, error) {
	if len(b) < frameHeaderLen {
		return Frame{}, ErrShort
	}
	if b[0] != magicFrame {
		return Frame{}, ErrMagic
	}
	if b[1] != Version {
		return Frame{}, ErrVersion
	}
	f := Frame{
		Quit: b[2]&flagQuit != 0,
		W:    int(binary.LittleEndian.Uint16(b[4:6])),
		H:    int(binary.LittleEndian.Uint16(b[6:8])),
	}
	n := f.W * f.H
	if len(b) < frameHeaderLen+n*cellLen {
		return Frame{}, ErrSize
	}
	f.Cells = make([]Cell, n)
	off := frameHeaderLen
	for i := 0; i < n; i++ {
		f.Cells[i] = Cell{
			Ch:    rune(binary.LittleEndian.Uint32(b[off : off+4])),
			Fg:    RGB{b[off+4], b[off+5], b[off+6]},
			Bg:    RGB{b[off+7], b[off+8], b[off+9]},
			Decor: b[off+10],
		}
		off += cellLen
	}
	return f, nil
}

// --- ANSI-string <-> structured conversion --------------------------------
//
// The current screen.Cell stores colors as truecolor SGR strings
// ("\x1b[38;2;r;g;bm" / "\x1b[48;2;r;g;bm") and decoration as "\x1b[..m". The
// guest converts those known forms to structured values here, so escapes never
// reach the wire. Anything that doesn't match falls back to ok=false and the
// caller substitutes a default — a hostile guest cannot smuggle arbitrary
// escape bytes through a color field.

// ParseSGRColor extracts the RGB from a "\x1b[38;2;r;g;bm" or
// "\x1b[48;2;r;g;bm" sequence. ok is false for any other shape.
func ParseSGRColor(s string) (RGB, bool) {
	body, ok := sgrBody(s)
	if !ok {
		return RGB{}, false
	}
	parts := strings.Split(body, ";")
	// 38;2;r;g;b  or  48;2;r;g;b
	if len(parts) != 5 || parts[1] != "2" || (parts[0] != "38" && parts[0] != "48") {
		return RGB{}, false
	}
	r, e1 := strconv.Atoi(parts[2])
	g, e2 := strconv.Atoi(parts[3])
	bl, e3 := strconv.Atoi(parts[4])
	if e1 != nil || e2 != nil || e3 != nil || !byteRange(r) || !byteRange(g) || !byteRange(bl) {
		return RGB{}, false
	}
	return RGB{uint8(r), uint8(g), uint8(bl)}, true
}

// ParseSGRDecor extracts decoration bits from a "\x1b[..m" sequence containing
// any of 1 (bold), 3 (italic), 4 (underline). Empty input means no decoration.
func ParseSGRDecor(s string) uint8 {
	if s == "" {
		return 0
	}
	body, ok := sgrBody(s)
	if !ok {
		return 0
	}
	var d uint8
	for _, p := range strings.Split(body, ";") {
		switch p {
		case "1":
			d |= DecorBold
		case "3":
			d |= DecorItalic
		case "4":
			d |= DecorUnderline
		}
	}
	return d
}

// sgrBody returns the parameter text of a "\x1b[<body>m" sequence.
func sgrBody(s string) (string, bool) {
	const prefix = "\x1b["
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, "m") {
		return "", false
	}
	return s[len(prefix) : len(s)-1], true
}

func byteRange(v int) bool { return v >= 0 && v <= 255 }

// FgSGR renders a foreground truecolor SGR string. Host-side only: the host
// builds terminal output from validated RGB, never from guest-supplied bytes.
func FgSGR(c RGB) string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B) }

// BgSGR renders a background truecolor SGR string (host-side only).
func BgSGR(c RGB) string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.R, c.G, c.B) }

// DecorSGR renders a decoration SGR string from decor bits (host-side only).
// Returns "" when no bits are set.
func DecorSGR(d uint8) string {
	if d == 0 {
		return ""
	}
	var parts []string
	if d&DecorBold != 0 {
		parts = append(parts, "1")
	}
	if d&DecorItalic != 0 {
		parts = append(parts, "3")
	}
	if d&DecorUnderline != 0 {
		parts = append(parts, "4")
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}
