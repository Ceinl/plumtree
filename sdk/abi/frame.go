package abi

import "encoding/binary"

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
	return AppendFrame(nil, f)
}

// AppendFrame serializes f into dst, reusing dst's allocation when it has
// enough capacity. Long-lived hosted TUI guests use this to keep repaint memory
// bounded instead of growing the WASM heap with a new frame buffer each tick.
func AppendFrame(dst []byte, f Frame) []byte {
	size := frameHeaderLen + len(f.Cells)*cellLen
	if cap(dst) < size {
		dst = make([]byte, size)
	} else {
		dst = dst[:size]
		clear(dst)
	}
	b := dst
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
	if len(b) != frameHeaderLen+n*cellLen {
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
