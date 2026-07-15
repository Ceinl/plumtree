package runner

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestInvalidLimitsReturnErrorsInsteadOfPanicking(t *testing.T) {
	for _, lim := range []Limits{
		{},
		{MemoryPages: MaxMemoryPages + 1},
		{MemoryPages: 1, SessionTimeout: -1},
		{MemoryPages: 1, MaxFramesPerSec: -1},
	} {
		if err := validateLimits(lim); err == nil {
			t.Errorf("validateLimits(%+v) succeeded", lim)
		}
	}
	if err := RunCLI(context.Background(), nil, Limits{}, Capabilities{}, nil, io.Discard); err == nil {
		t.Fatal("RunCLI accepted zero memory limit")
	} else if errors.Is(err, context.Canceled) {
		t.Fatalf("RunCLI returned unrelated error: %v", err)
	}
}

// A guest that yields every frame but never finishes is killed at the total
// session budget, even though no single frame trips the per-frame watchdog.
// busyguest spins in compute after its first event, so the session deadline
// fires while it is busy (not blocked in recv).
func TestRunSessionDeadline(t *testing.T) {
	wasm := buildGuest(t, "testdata/busyguest", "GOWORK=off")

	lim := Limits{
		MemoryPages:    512,
		FrameTimeout:   10 * time.Second,       // large: must not fire first
		SessionTimeout: 150 * time.Millisecond, // the budget under test
	}
	src := NewScriptSource(10, 3, []string{"up"})

	start := time.Now()
	err := Run(context.Background(), wasm, lim, Capabilities{}, src, &capture{}, io.Discard)
	if err != ErrSessionDeadline {
		t.Fatalf("Run err = %v, want ErrSessionDeadline", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("session termination took %s, budget was 150ms", elapsed)
	}
}

// With a tight output budget, frames presented in a burst are dropped: the sink
// sees far fewer frames than the guest produced.
func TestRunDropsFramesOverOutputBudget(t *testing.T) {
	wasm := buildGuest(t, "../sdk/examples/counter")

	lim := DefaultLimits
	lim.MaxFramesPerSec = 1 // burst 1: only the first present in a burst passes

	var sink capture
	src := NewScriptSource(24, 6, []string{"up", "up", "up", "q"})
	if err := Run(context.Background(), wasm, lim, Capabilities{}, src, &sink, io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Unthrottled this guest presents 5 frames (initial + 4 events). The burst-1
	// budget should drop most of them.
	if len(sink.frames) >= 5 {
		t.Fatalf("got %d frames, expected dropping below 5", len(sink.frames))
	}
	if len(sink.frames) == 0 {
		t.Fatal("at least the first frame should pass the output budget")
	}
}
