package runner

import (
	"bytes"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

var malformedFrames = []struct {
	name  string
	frame abi.Frame
}{
	{"zero width", abi.Frame{W: 0, H: 1, Cells: []abi.Cell{{Ch: 'A'}}}},
	{"negative width", abi.Frame{W: -2, H: 2, Cells: make([]abi.Cell, 8)}},
	{"over-max width", abi.Frame{W: abi.MaxFrameWidth + 1, H: 1, Cells: []abi.Cell{{Ch: 'A'}}}},
	{"over-max height", abi.Frame{W: 1, H: abi.MaxFrameHeight + 1, Cells: nil}},
	{"cell-count mismatch", abi.Frame{W: 2, H: 2, Cells: []abi.Cell{{Ch: 'o'}, {Ch: 'l'}}}},
	{"nil cells", abi.Frame{W: 2, H: 2, Cells: nil}},
}

// A malformed frame must be ignored whole: no partial write reaches the
// terminal and the sink stays healthy for the next valid frame. The contract
// holds for every sink a guest frame can reach.
func TestPresentRejectsMalformedFrames(t *testing.T) {
	t.Run("tty-sink", func(t *testing.T) {
		var out bytes.Buffer
		sink := NewTTYSinkWriter(4, 2, 0, &out)
		for _, test := range malformedFrames {
			t.Run(test.name, func(t *testing.T) {
				out.Reset()
				sink.Present(test.frame)
				if out.Len() != 0 {
					t.Fatalf("malformed frame wrote %q", out.String())
				}
			})
		}
		if !sink.Healthy() {
			t.Fatal("ignored frames marked the sink unhealthy")
		}
		presentValidFrame(t, sink, &out)
	})

	t.Run("text-sink", func(t *testing.T) {
		var out bytes.Buffer
		sink := TextSink{W: &out}
		for _, test := range malformedFrames {
			t.Run(test.name, func(t *testing.T) {
				out.Reset()
				sink.Present(test.frame)
				if out.Len() != 0 {
					t.Fatalf("malformed frame wrote %q", out.String())
				}
			})
		}
		presentValidFrame(t, sink, &out)
	})
}

type frameSink interface{ Present(abi.Frame) }

func presentValidFrame(t *testing.T, sink frameSink, out *bytes.Buffer) {
	t.Helper()
	sink.Present(abi.Frame{W: 1, H: 1, Cells: []abi.Cell{{Ch: 'A'}}})
	if !bytes.Contains(out.Bytes(), []byte("A")) {
		t.Errorf("valid frame after malformed ones did not render: %q", out.String())
	}
}
