package terminal

import (
	"fmt"
	"io"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func BenchmarkScreenFlush(b *testing.B) {
	for _, size := range [][2]int{{120, 40}, {MaxWidth, MaxHeight}} {
		for _, mode := range []string{"unchanged", "sparse", "full"} {
			b.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], mode), func(b *testing.B) {
				s := NewScreenWithOutput(size[0], size[1], io.Discard)
				s.Flush()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					cell := abi.Cell{Ch: rune('a' + i%2), Fg: abi.RGB{R: 255, G: 128, B: 64}}
					switch mode {
					case "sparse":
						s.Set(size[0]-1, size[1]-1, cell)
					case "full":
						for y := range s.cur {
							for x := range s.cur[y] {
								cell.Fg.G = uint8(x + i)
								s.Set(x, y, cell)
							}
						}
					}
					s.Flush()
				}
			})
		}
	}
}
