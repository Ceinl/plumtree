// Package abi defines the versioned wire format exchanged between a Plumtree
// guest app (WASM) and the host runner across guest linear memory.
//
// The guest drives its own loop, calling two host-imported functions: it asks
// the host for the next input (an encoded Event) and hands back a render (an
// encoded Frame). Two messages exist:
//
//   - Event  (host -> guest): an input event — a key, a resize, or "none".
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
	"errors"
)

// Version is the ABI wire version. Bump on any incompatible layout change.
//
// v1 added the KV host capability (kv_get/kv_set/kv_delete). The change is
// additive — the Event/Frame layout is unchanged — but host and guest are
// always built together, so the version moves in lockstep.
//
// v2 added the pub/sub capability (bus_pub/bus_sub) and the KindMessage event
// that delivers a published message to a subscribed guest. KindMessage is the
// first variable-length Event: it carries a topic and payload after the fixed
// header, so DecodeEvent reads past EventLen for that kind only.
// v3 adds KindMouse. The fixed header remains 13 bytes; mouse events encode
// button/action plus cell coordinates in fields unused by other event kinds.
const Version uint8 = 3

const (
	magicEvent byte = 0x01
	magicFrame byte = 0x02
)

// Kind discriminates Event payloads.
type Kind uint8

const (
	// KindNone is a repaint/tick with no input.
	KindNone Kind = 0
	// KindKey is a keyboard event.
	KindKey Kind = 1
	// KindResize reports a new terminal size in W/H (cells).
	KindResize Kind = 2
	// KindMessage delivers a pub/sub message published by another session of the
	// same app. Topic and Data carry the payload; it is variable length on the
	// wire (see EncodeEvent).
	KindMessage Kind = 3
	// KindMouse reports a terminal mouse action at zero-based cell coordinates.
	KindMouse Kind = 4
)

type MouseButton uint8

const (
	MouseButtonNone MouseButton = iota
	MouseButtonLeft
)

type MouseAction uint8

const (
	MouseDown MouseAction = iota + 1
	MouseUp
	MouseDrag
	MouseWheelUp
	MouseWheelDown
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

// Event is an input event sent host -> guest. W/H are set only for KindResize;
// Topic/Data only for KindMessage.
type Event struct {
	Kind           Kind
	Key            KeyType
	Ch             rune
	Mods           Mods
	W, H           int
	Topic          string // KindMessage: the topic the message was published to
	Data           []byte // KindMessage: the message payload
	MouseX, MouseY int
	Button         MouseButton
	Action         MouseAction
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

// Pub/sub message size caps. A KindMessage event is delivered inline in the
// guest's recv buffer, so its encoded size must stay within that buffer; the
// caps below keep the worst-case wire size well under the guest's evbuf.
const (
	// BusMaxTopic caps a topic name's length in bytes.
	BusMaxTopic = 128
	// BusMaxData caps a published payload's length in bytes.
	BusMaxData = 3072
)

// Bus result codes for bus_sub / bus_pub. A non-negative bus_pub return is the
// number of subscribers reached; only negative returns are errors.
const (
	BusOk          int32 = 0
	BusErrTooLarge int32 = -1 // topic or payload exceeds its cap
	BusErrInternal int32 = -2 // host-side failure or absent capability
)
