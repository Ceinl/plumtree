package abi

import "encoding/binary"

const frameHeaderLen = 8 // magic, version, flags, reserved, w(2), h(2)
const cellLen = 11       // rune(4) + fg(3) + bg(3) + decor(1)

// Frame limits bound host allocation and match the supported terminal size.
const (
	MaxFrameWidth  = 500
	MaxFrameHeight = 300
	MaxFrameBytes  = frameHeaderLen + MaxFrameWidth*MaxFrameHeight*cellLen
)

const flagQuit byte = 1 << 0

// EncodeFrame serializes a structured frame. Header (little-endian):
//
//	[0] magic=0x02  [1] version  [2] flags  [3] reserved
//	[4:6] w uint16  [6:8] h uint16
//
// followed by W*H cells, each: rune int32 | fg RGB | bg RGB | decor uint8.
func EncodeFrame(f Frame) []byte {
	return AppendFrame(nil, f)
}

// AppendFrame serializes f into dst, reusing dst's allocation when it has
// enough capacity. Long-lived hosted TUI guests use this to keep repaint memory
// bounded instead of growing the WASM heap with a new frame buffer each tick.
func AppendFrame(dst []byte, f Frame) []byte {
	size := frameHeaderLen + len(f.Cells)*cellLen
	if cap(dst) < size {
		dst = make([]byte, size)
	}
	dst = dst[:size]
	b := dst
	b[0] = magicFrame
	b[1] = Version
	b[2] = 0
	if f.Quit {
		b[2] = flagQuit
	}
	b[3] = 0
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
	f, _, err := AppendDecodeFrame(nil, b)
	return f, err
}

// AppendDecodeFrame parses b into a Frame whose cells are stored in dst,
// reusing dst's backing array when it has enough capacity. The returned slice
// is the Frame's cell storage; it remains valid only until the next
// AppendDecodeFrame call on the same buffer, so callers must not retain it past
// handing the Frame to its consumer.
func AppendDecodeFrame(dst []Cell, b []byte) (Frame, []Cell, error) {
	if len(b) < frameHeaderLen {
		return Frame{}, dst, ErrShort
	}
	if b[0] != magicFrame {
		return Frame{}, dst, ErrMagic
	}
	if b[1] != Version {
		return Frame{}, dst, ErrVersion
	}
	f := Frame{
		Quit: b[2]&flagQuit != 0,
		W:    int(binary.LittleEndian.Uint16(b[4:6])),
		H:    int(binary.LittleEndian.Uint16(b[6:8])),
	}
	if f.W < 1 || f.W > MaxFrameWidth || f.H < 1 || f.H > MaxFrameHeight {
		return Frame{}, dst, ErrSize
	}
	n := f.W * f.H
	if len(b) != frameHeaderLen+n*cellLen {
		return Frame{}, dst, ErrSize
	}
	if cap(dst) < n {
		dst = make([]Cell, n)
	}
	dst = dst[:n]
	off := frameHeaderLen
	for i := range dst {
		dst[i] = Cell{
			Ch:    rune(binary.LittleEndian.Uint32(b[off : off+4])),
			Fg:    RGB{b[off+4], b[off+5], b[off+6]},
			Bg:    RGB{b[off+7], b[off+8], b[off+9]},
			Decor: b[off+10],
		}
		off += cellLen
	}
	f.Cells = dst
	return f, dst, nil
}
