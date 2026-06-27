package gateway

import "testing"

func TestAcquireSlotRespectsCap(t *testing.T) {
	s := &Server{MaxConcurrentSessions: 2}
	s.slots = make(chan struct{}, s.MaxConcurrentSessions)

	if !s.acquireSlot() || !s.acquireSlot() {
		t.Fatal("first two slots should be granted")
	}
	if s.acquireSlot() {
		t.Fatal("third slot should be rejected at the cap")
	}

	s.releaseSlot()
	if !s.acquireSlot() {
		t.Fatal("a slot should be available after release")
	}
}

func TestAcquireSlotUnlimited(t *testing.T) {
	s := &Server{} // MaxConcurrentSessions == 0, slots nil
	for i := 0; i < 1000; i++ {
		if !s.acquireSlot() {
			t.Fatal("unlimited server must always grant slots")
		}
	}
	s.releaseSlot() // must be a no-op, not panic
}
