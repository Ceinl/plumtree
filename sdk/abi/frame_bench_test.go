package abi

import (
	"fmt"
	"testing"
)

func benchFrame(w, h int) Frame {
	f := Frame{W: w, H: h, Cells: make([]Cell, w*h)}
	for i := range f.Cells {
		f.Cells[i] = Cell{Ch: 'x', Fg: RGB{R: 200, G: 200, B: 200}, Bg: RGB{R: 25, G: 23, B: 29}}
	}
	return f
}

func BenchmarkFrameEncode(b *testing.B) {
	for _, size := range [][2]int{{160, 48}, {500, 300}} {
		b.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(b *testing.B) {
			f := benchFrame(size[0], size[1])
			dst := make([]byte, 0, MaxFrameBytes)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst = AppendFrame(dst[:0], f)
			}
		})
	}
}

func BenchmarkFrameDecode(b *testing.B) {
	for _, size := range [][2]int{{160, 48}, {500, 300}} {
		b.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(b *testing.B) {
			raw := EncodeFrame(benchFrame(size[0], size[1]))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := DecodeFrame(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFrameDecodeAppend is the host-pipeline shape: decode into a
// per-session buffer that is reused across frames.
func BenchmarkFrameDecodeAppend(b *testing.B) {
	for _, size := range [][2]int{{160, 48}, {500, 300}} {
		b.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(b *testing.B) {
			raw := EncodeFrame(benchFrame(size[0], size[1]))
			var cells []Cell
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var err error
				_, cells, err = AppendDecodeFrame(cells, raw)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
