package app

import (
	"context"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/ui"
)

func TestRuntimeFrameRemainsDetachedAfterDispatch(t *testing.T) {
	r := NewRuntime(&testModel{})
	defer r.Stop()
	if err := r.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := r.Frame()
	text := before.Text()
	if err := r.Dispatch(incrementEvent{}); err != nil {
		t.Fatal(err)
	}
	if r.Frame().Text() == text {
		t.Fatal("dispatch did not change the view")
	}
	if before.Text() != text {
		t.Fatal("dispatch changed a public frame snapshot")
	}
}

// BenchmarkRuntimeDispatch includes Update, View construction, rendering, and
// focus reconciliation. It excludes guest encoding and host transport.
func BenchmarkRuntimeDispatch(b *testing.B) {
	r := NewRuntime(&testModel{}, Viewport(160, 48))
	defer r.Stop()
	if err := r.Init(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.Dispatch(incrementEvent{}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestAppendFrameKeepsEncodedOutputDetached(t *testing.T) {
	r := &Runtime{frame: ui.Render(ui.Text("hello"), 20, 4), quit: true}
	converter := &cleanFrameConverter{}
	encoded := r.appendFrame(nil, converter)
	r.frame = ui.Render(ui.Text("other"), 20, 4)
	r.appendFrame(nil, converter)
	frame, err := abi.DecodeFrame(encoded)
	if err != nil || !frame.Quit || frame.W != 20 || frame.H != 4 {
		t.Fatalf("frame=%+v err=%v", frame, err)
	}
	for i, ch := range "hello" {
		if frame.Cells[i].Ch != ch {
			t.Fatal("earlier output changed")
		}
	}
}

func BenchmarkHostedFrameEncoding(b *testing.B) {
	for _, clone := range []bool{true, false} {
		name := "direct"
		if clone {
			name = "snapshot"
		}
		b.Run(name, func(b *testing.B) {
			r := &Runtime{frame: ui.Render(ui.Text("hello"), 160, 48)}
			converter := &cleanFrameConverter{}
			encoded := r.appendFrame(nil, converter)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if clone {
					encoded = abi.AppendFrame(encoded[:0], converter.frame(r.Frame(), false))
				} else {
					encoded = r.appendFrame(encoded[:0], converter)
				}
			}
		})
	}
}
