package main

import (
	"errors"
	"testing"

	"github.com/Ceinl/plumtree/sdk"
)

func TestTimerAdvancesAnimation(t *testing.T) {
	m := &saver{timer: 7}
	m.Update(sdk.TimerMsg{ID: 6})
	if m.frame != 0 {
		t.Fatal("unrelated timer advanced animation")
	}
	m.Update(sdk.TimerMsg{ID: 7})
	if m.frame != 1 {
		t.Fatalf("frame = %d, want 1", m.frame)
	}
}

func TestScheduleFailureIsReported(t *testing.T) {
	m, err := newSaver(func(sdk.Command) (sdk.CommandID, error) {
		return 0, sdk.ErrCommandUnavailable
	})
	if m != nil {
		t.Fatalf("model = %+v, want nil", m)
	}
	if !errors.Is(err, sdk.ErrCommandUnavailable) {
		t.Fatalf("error = %v, want %v", err, sdk.ErrCommandUnavailable)
	}
}

func TestStarsTwinkleDeterministically(t *testing.T) {
	changed := false
	for y := 0; y < 40; y++ {
		for x := 0; x < 100; x++ {
			first := starAt(x, y, 0)
			if first != starAt(x, y, 0) {
				t.Fatal("star field is not deterministic")
			}
			changed = changed || first != starAt(x, y, 4)
		}
	}
	if !changed {
		t.Fatal("star field did not change phase")
	}
}
