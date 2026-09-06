package abi

import "testing"

func TestDecodeFrameRejectsOversizedDimensions(t *testing.T) {
	for _, size := range [][2]int{{MaxFrameWidth + 1, 1}, {1, MaxFrameHeight + 1}} {
		f := Frame{W: size[0], H: size[1], Cells: make([]Cell, size[0]*size[1])}
		if _, err := DecodeFrame(EncodeFrame(f)); err != ErrSize {
			t.Fatalf("dimensions %v: error=%v", size, err)
		}
	}
}
