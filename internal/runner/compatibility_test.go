package runner

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	baselineABIV4CounterSize   = 3350824
	baselineABIV4CounterSHA256 = "b6c898405cc3526b2b6f0395ada7af5d2ea7344987c822778360938acdd3b482"
)

type compatRunFunc func(context.Context, []byte, Limits, Capabilities, Source, Sink, io.Writer) error

func TestBaselineABIV4CounterArtifact(t *testing.T) {
	wasm := readBaselineABIV4Counter(t)

	t.Run("in-process", func(t *testing.T) {
		assertBaselineABIV4Counter(t, wasm, Run)
	})
	t.Run("isolated-worker", func(t *testing.T) {
		worker := buildWorker(t)
		assertBaselineABIV4Counter(t, wasm, NewProcessRunner(worker).Run)
	})
}

func readBaselineABIV4Counter(t *testing.T) []byte {
	t.Helper()
	f, err := os.Open("testdata/compat/abi-v4-counter.wasm.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	wasm, err := io.ReadAll(io.LimitReader(zr, baselineABIV4CounterSize+1))
	if err != nil {
		t.Fatal(err)
	}
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}
	if len(wasm) != baselineABIV4CounterSize {
		t.Fatalf("baseline artifact size = %d, want %d", len(wasm), baselineABIV4CounterSize)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(wasm)); got != baselineABIV4CounterSHA256 {
		t.Fatalf("baseline artifact SHA-256 = %s, want %s", got, baselineABIV4CounterSHA256)
	}
	return wasm
}

func assertBaselineABIV4Counter(t *testing.T, wasm []byte, run compatRunFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var sink capture
	src := NewScriptSource(24, 6, []string{"up", "up", "down", "q"})
	if err := run(ctx, wasm, DefaultLimits, Capabilities{}, src, &sink, io.Discard); err != nil {
		t.Fatal(err)
	}

	wantCounts := []string{"Count: 0", "Count: 1", "Count: 2", "Count: 1", "Count: 1"}
	if len(sink.frames) != len(wantCounts) {
		t.Fatalf("got %d frames, want %d", len(sink.frames), len(wantCounts))
	}
	for i, want := range wantCounts {
		got := frameText(sink.frames[i])
		if !hasTrimmedLine(got, want) {
			t.Errorf("frame %d missing exact line %q:\n%s", i, want, got)
		}
	}
	if !sink.frames[len(sink.frames)-1].Quit {
		t.Error("final frame should carry the quit flag")
	}
}

func hasTrimmedLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func TestHasTrimmedLineRequiresExactValue(t *testing.T) {
	if !hasTrimmedLine("  Count: 1  \n", "Count: 1") {
		t.Error("exact padded line was not found")
	}
	if hasTrimmedLine("Count: 10\n", "Count: 1") {
		t.Error("Count: 10 matched Count: 1")
	}
}
