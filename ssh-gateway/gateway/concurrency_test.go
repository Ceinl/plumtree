package gateway

import (
	"context"
	"testing"

	"github.com/Ceinl/plumtree/runner"
)

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

func TestStartSessionAcquiresCapacityBeforeResolvingArtifact(t *testing.T) {
	backend := &countingBackend{}
	s := &Server{Backend: backend, MaxConcurrentSessions: 1}
	s.slots = make(chan struct{}, 1)
	s.slots <- struct{}{} // occupy the only runner slot
	ch := &testChannel{}
	ctx, cancel := context.WithCancel(context.Background())

	s.startSession(ctx, cancel, ch, "alice/app", runner.Identity{}, nil, nil)

	if backend.resolveCalls != 0 {
		t.Fatalf("ResolveRunnable called %d times while at capacity, want 0", backend.resolveCalls)
	}
}

type countingBackend struct{ resolveCalls int }

func (*countingBackend) ResolveIdentity(fingerprint string) (runner.Identity, error) {
	return runner.Identity{User: fingerprint}, nil
}
func (b *countingBackend) ResolveRunnable(string) (Runnable, error) {
	b.resolveCalls++
	return Runnable{}, nil
}
func (*countingBackend) StartSession(string, string) (string, error) { return "", nil }
func (*countingBackend) RecordSessionLog(string, string, bool) error { return nil }
func (*countingBackend) EndSession(string) error                     { return nil }
func (*countingBackend) SecretsForApp(string) map[string]string      { return nil }
func (*countingBackend) EgressAllowlist(string) []string             { return nil }

func TestAcquireSlotUnlimited(t *testing.T) {
	s := &Server{} // MaxConcurrentSessions == 0, slots nil
	for i := 0; i < 1000; i++ {
		if !s.acquireSlot() {
			t.Fatal("unlimited server must always grant slots")
		}
	}
	s.releaseSlot() // must be a no-op, not panic
}
