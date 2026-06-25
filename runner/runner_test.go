package runner

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
)

// A Runner runs the same module across many sessions, reusing compiled code via
// its shared cache. Each session must still produce correct, independent output.
func TestRunnerReusesAcrossSessions(t *testing.T) {
	wasm := buildGuest(t, "../sdk/examples/counter")

	rn := New()
	defer rn.Close(context.Background())

	for session := 0; session < 3; session++ {
		var sink capture
		src := NewScriptSource(24, 6, []string{"up", "up", "down", "q"})
		if err := rn.Run(context.Background(), wasm, DefaultLimits, Capabilities{}, src, &sink, io.Discard); err != nil {
			t.Fatalf("session %d Run: %v", session, err)
		}
		wantCounts := []string{"Count: 0", "Count: 1", "Count: 2", "Count: 1", "Count: 1"}
		if len(sink.frames) != len(wantCounts) {
			t.Fatalf("session %d: got %d frames, want %d", session, len(sink.frames), len(wantCounts))
		}
		for i, want := range wantCounts {
			if got := frameText(sink.frames[i]); !strings.Contains(got, want) {
				t.Errorf("session %d frame %d missing %q:\n%s", session, i, want, got)
			}
		}
	}
}

// Concurrent sessions on one Runner share only immutable compiled code, never
// guest state: each session's counter must reflect only its own input.
func TestRunnerConcurrentSessionsIsolated(t *testing.T) {
	wasm := buildGuest(t, "../sdk/examples/counter")

	rn := New()
	defer rn.Close(context.Background())

	cases := []struct {
		tokens []string
		want   string // final counter value before quit
	}{
		{[]string{"up", "up", "up", "q"}, "Count: 3"},
		{[]string{"up", "q"}, "Count: 1"},
		{[]string{"up", "up", "down", "q"}, "Count: 1"},
		{[]string{"q"}, "Count: 0"},
	}

	var wg sync.WaitGroup
	errs := make([]error, len(cases))
	gots := make([]string, len(cases))
	for i, c := range cases {
		wg.Add(1)
		go func(i int, tokens []string) {
			defer wg.Done()
			var sink capture
			src := NewScriptSource(24, 6, tokens)
			if err := rn.Run(context.Background(), wasm, DefaultLimits, Capabilities{}, src, &sink, io.Discard); err != nil {
				errs[i] = err
				return
			}
			if len(sink.frames) > 0 {
				// The frame before the final quit holds the last counter value.
				gots[i] = frameText(sink.frames[len(sink.frames)-1])
			}
		}(i, c.tokens)
	}
	wg.Wait()

	for i, c := range cases {
		if errs[i] != nil {
			t.Errorf("case %d (%v): Run: %v", i, c.tokens, errs[i])
			continue
		}
		if !strings.Contains(gots[i], c.want) {
			t.Errorf("case %d (%v): final frame missing %q:\n%s", i, c.tokens, c.want, gots[i])
		}
	}
}
