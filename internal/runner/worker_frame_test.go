package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestWorkerForwardsEncodedFrameToParent(t *testing.T) {
	frame := abi.Frame{W: 2, H: 1, Quit: true, Cells: []abi.Cell{
		{Ch: 'λ', Fg: abi.RGB{R: 17}, Decor: abi.DecorBold}, {Ch: 'x'},
	}}
	raw := abi.EncodeFrame(frame)
	var reply, wire bytes.Buffer
	if err := writeMsg(&reply, opResp, nil); err != nil {
		t.Fatal(err)
	}
	rpc := workerRPC{in: &reply, out: &wire}
	rpc.present(raw)
	op, payload, err := readMsg(&wire)
	if err != nil || op != opPresent || !bytes.Equal(payload, raw) {
		t.Fatalf("frame changed in transit: op=%v err=%v payload=%x", op, err, payload)
	}
	var sink capture
	pr := ProcessRunner{}
	if err := pr.serve(context.Background(), io.Discard, op, payload, &procSession{}, Capabilities{}, nil, &sink, nil, CLIStreams{}); err != nil {
		t.Fatal(err)
	}
	if len(sink.frames) != 1 || !bytes.Equal(abi.EncodeFrame(sink.frames[0]), raw) {
		t.Fatal("parent did not deliver the original frame")
	}
}

func TestParentRejectsMalformedWorkerFrames(t *testing.T) {
	valid := abi.EncodeFrame(abi.Frame{W: 1, H: 1, Cells: []abi.Cell{{Ch: 'x'}}})
	badMagic := bytes.Clone(valid)
	badMagic[0] = 0
	badSize := bytes.Clone(valid)
	badSize[4], badSize[5] = 0xff, 0xff
	for _, raw := range [][]byte{nil, valid[:len(valid)-1], badMagic, badSize} {
		var sink capture
		pr := ProcessRunner{}
		err := pr.serve(context.Background(), io.Discard, opPresent, raw, &procSession{}, Capabilities{}, nil, &sink, nil, CLIStreams{})
		if !errors.Is(err, errProtocol) || len(sink.frames) != 0 {
			t.Fatalf("malformed frame reached sink: err=%v frames=%d", err, len(sink.frames))
		}
	}
}
