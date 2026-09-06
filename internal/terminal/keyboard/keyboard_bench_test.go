package keyboard

import (
	"bytes"
	"context"
	"testing"
)

// benchScript builds one iteration's worth of input: a realistic mix of ASCII
// runes, multi-byte runes, arrow/CSI sequences, and SGR mouse reports.
func benchScript(n int) []byte {
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		switch i % 8 {
		case 0:
			buf.WriteByte('a')
		case 1:
			buf.WriteString("\x1b[A") // up
		case 2:
			buf.WriteString("\x1b[B") // down
		case 3:
			buf.WriteString("héllo") // multi-byte
		case 4:
			buf.WriteByte('\r')
		case 5:
			buf.WriteString("\x1b[<0;10;5M") // mouse press
		case 6:
			buf.WriteString("\x1b[<0;10;5m") // mouse release
		case 7:
			buf.WriteByte('z')
		}
	}
	return buf.Bytes()
}

// BenchmarkKeyboardPipeline measures the full listen → parse → deliver path for
// one script of keystrokes (12 events from 8 input units), read from a bytes.Reader.
func BenchmarkKeyboardPipeline(b *testing.B) {
	const units, expectedEvents = 8, 12
	input := benchScript(units)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		eventsCh := ListenReader(ctx, bytes.NewReader(input))
		got := 0
		for range eventsCh {
			got++
		}
		cancel()
		if got != expectedEvents {
			b.Fatalf("got %d events, want %d", got, expectedEvents)
		}
	}
}
