package keyboard

import (
	"bytes"
	"time"
)

var bracketedPasteEnd = []byte("\x1b[201~")

func readBracketedPaste(reader *byteReader) Event {
	buf := make([]byte, 0, 256)
	for {
		b, ok := reader.readByteTimeout(50 * time.Millisecond)
		if !ok {
			return Event{Type: KeyPaste, Text: string(buf)}
		}
		buf = append(buf, b)
		if bytes.HasSuffix(buf, bracketedPasteEnd) {
			buf = buf[:len(buf)-len(bracketedPasteEnd)]
			return Event{Type: KeyPaste, Text: string(buf)}
		}
	}
}
