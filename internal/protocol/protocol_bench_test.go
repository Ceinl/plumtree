package protocol

import (
	"bytes"
	"fmt"
	"testing"
)

type countWriter struct {
	buf   bytes.Buffer
	calls int
}

func (w *countWriter) Write(p []byte) (int, error) {
	w.calls++
	return w.buf.Write(p)
}

func BenchmarkProtocolRoundTrip(b *testing.B) {
	for _, size := range []int{14, 4096, 1 << 20} {
		b.Run(fmt.Sprintf("payload=%d", size), func(b *testing.B) {
			payload := make([]byte, size)
			w := &countWriter{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				w.buf.Reset()
				if err := Write(w, OpPresent, payload); err != nil {
					b.Fatal(err)
				}
				if _, _, err := ReadBounded(bytes.NewReader(w.buf.Bytes()), nil); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(w.calls)/float64(b.N), "writes/msg")
		})
	}
}
