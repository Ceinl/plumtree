package runner

import (
	"fmt"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

type renderWriter struct{ calls int }

func (w *renderWriter) Write(p []byte) (int, error) { w.calls++; return len(p), nil }
func BenchmarkTTYSink(b *testing.B) {
	for _, size := range [][2]int{{80, 24}, {160, 48}, {500, 300}} {
		for _, changed := range []bool{false, true} {
			b.Run(fmt.Sprintf("%dx%d/changed=%t", size[0], size[1], changed), func(b *testing.B) {
				w := &renderWriter{}
				s := NewTTYSinkWriter(size[0], size[1], 0, w)
				defer s.Close()
				f := abi.Frame{W: size[0], H: size[1], Cells: make([]abi.Cell, size[0]*size[1])}
				for i := range f.Cells {
					f.Cells[i] = abi.Cell{Ch: 'x', Fg: abi.RGB{R: 200, G: 200, B: 200}, Bg: abi.RGB{R: 25, G: 23, B: 29}}
				}
				s.Present(f)
				w.calls = 0
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if changed {
						for j := range f.Cells {
							f.Cells[j].Ch = rune('a' + i%2)
						}
					}
					s.Present(f)
				}
				b.StopTimer()
				b.ReportMetric(float64(w.calls)/float64(b.N), "writes/frame")
			})
		}
	}
}
