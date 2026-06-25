package main

import (
	"testing"

	"github.com/Ceinl/plumtree/sdk"
)

func TestCounterUpdate(t *testing.T) {
	c := &counter{}
	c.Update(sdk.KeyMsg{Key: sdk.KeyUp})
	c.Update(sdk.KeyMsg{Key: '+'})
	if c.n != 2 {
		t.Errorf("after up,+ : n=%d want 2", c.n)
	}
	c.Update(sdk.KeyMsg{Key: sdk.KeyDown})
	if c.n != 1 {
		t.Errorf("after down: n=%d want 1", c.n)
	}
	// Non-key events are ignored.
	c.Update(sdk.ResizeMsg{W: 80, H: 24})
	if c.n != 1 {
		t.Errorf("resize changed n to %d", c.n)
	}
}

func TestCounterViewBuilds(t *testing.T) {
	c := &counter{n: 7}
	if c.View() == nil {
		t.Fatal("View returned nil")
	}
}
