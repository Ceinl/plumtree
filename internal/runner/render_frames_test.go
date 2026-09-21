package runner

import (
	"bytes"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// A malformed frame must be ignored whole: no partial write reaches the
// terminal and the sink stays healthy for the next valid frame.
func TestPresentRejectsMalformedFrames(t *testing.T) {
	var out bytes.Buffer
	sink := NewTTYSinkWriter(4, 2, 0, &out)
	bad := []abi.Frame{
		{W: 0, H: 1, Cells: []abi.Cell{{Ch: 'A'}}},
		{W: -2, H: 2, Cells: make([]abi.Cell, 8)},
		{W: abi.MaxFrameWidth + 1, H: 1, Cells: []abi.Cell{{Ch: 'A'}}},
		{W: 1, H: abi.MaxFrameHeight + 1, Cells: nil},
		{W: 2, H: 2, Cells: []abi.Cell{{Ch: 'o'}, {Ch: 'l'}}},
		{W: 2, H: 2, Cells: nil},
	}
	for i, frame := range bad {
		t.Run("case"+string(rune('a'+i)), func(t *testing.T) {
			sink.Present(frame)
			if out.Len() != 0 {
				t.Fatalf("malformed frame wrote %q", out.String())
			}
		})
	}
	if !sink.Healthy() {
		t.Fatal("ignored frames marked the sink unhealthy")
	}
	sink.Present(abi.Frame{W: 1, H: 1, Cells: []abi.Cell{{Ch: 'A'}}})
	sink.Present(abi.Frame{W: 1, H: 1, Cells: []abi.Cell{{Ch: 'A'}}})
	if !bytes.Contains(out.Bytes(), []byte("A")) {
		t.Fatalf("valid frame after malformed ones did not render: %q", out.String())
	}
}
