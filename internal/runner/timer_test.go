package runner

import (
	"context"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestTimerManagerOneShotAndRecurring(t *testing.T) {
	manager := newTimerManager(context.Background())
	defer manager.Close()

	once := manager.Start(time.Millisecond, false)
	every := manager.Start(10*time.Millisecond, true)
	if once <= 0 || every <= 0 {
		t.Fatalf("timer IDs = %d, %d", once, every)
	}

	seen := map[uint32]int{}
	deadline := time.After(time.Second)
	for seen[uint32(once)] < 1 || seen[uint32(every)] < 2 {
		select {
		case event := <-manager.Events():
			if event.Kind != abi.KindTimer {
				t.Fatalf("event kind = %v", event.Kind)
			}
			seen[event.CommandID]++
		case <-deadline:
			t.Fatalf("timers did not fire: %+v", seen)
		}
	}
	if manager.Cancel(uint32(once)) {
		t.Error("completed one-shot remained active")
	}
	if !manager.Cancel(uint32(every)) {
		t.Error("recurring timer was not active")
	}
}

func TestTimerManagerBoundsScheduling(t *testing.T) {
	manager := newTimerManager(context.Background())
	defer manager.Close()

	if got := manager.Start(0, false); got != abi.TimerErrInvalid {
		t.Fatalf("zero delay = %d, want invalid", got)
	}
	for range abi.TimerMaxActive {
		if got := manager.Start(time.Hour, false); got <= 0 {
			t.Fatalf("timer within limit = %d", got)
		}
	}
	if got := manager.Start(time.Hour, false); got != abi.TimerErrLimit {
		t.Fatalf("timer over limit = %d, want limit", got)
	}
}
