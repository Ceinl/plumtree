package gateway

import (
	"context"
	"testing"

	"github.com/Ceinl/plumtree/internal/runner"
)

func TestAcquireSlotRespectsCap(t *testing.T) {
	s := mustNewServer(t, Config{Backend: &countingBackend{}, MaxConcurrentSessions: 2})

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
	s := mustNewServer(t, Config{Backend: backend, MaxConcurrentSessions: 1})
	if !s.acquireSlot() { // occupy the only runner slot
		t.Fatal("could not occupy the only runner slot")
	}
	ch := &testChannel{}
	ctx, cancel := context.WithCancel(context.Background())

	s.startSession(ctx, cancel, ch, "alice/app", runner.Identity{}, nil, nil)

	if backend.resolveCalls != 0 {
		t.Fatalf("ResolveRunnable called %d times while at capacity, want 0", backend.resolveCalls)
	}
}

type countingBackend struct{ resolveCalls int }

func (*countingBackend) ResolveIdentity(_ context.Context, fingerprint string) (runner.Identity, error) {
	return runner.Identity{User: fingerprint, Kind: runner.IdentitySSHKey}, nil
}
func (b *countingBackend) ResolveRunnable(context.Context, string, runner.Identity) (Runnable, error) {
	b.resolveCalls++
	return Runnable{}, nil
}
func (*countingBackend) StartSession(context.Context, string, string, string, string) (string, error) {
	return "", nil
}
func (*countingBackend) RecordSessionLog(context.Context, string, string, bool) error { return nil }
func (*countingBackend) EndSession(context.Context, string) error                     { return nil }
func (*countingBackend) SecretsForApp(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (*countingBackend) EgressAllowlist(context.Context, string) ([]string, error) {
	return nil, nil
}
func (*countingBackend) KVStore(context.Context, string) (runner.Store, error) {
	return nil, ErrCapsUnavailable
}
func (*countingBackend) StartSuspensionWatcher(context.Context, func(context.Context, Suspension) error) error {
	return nil
}

func TestAcquireSlotUnlimited(t *testing.T) {
	s := mustNewServer(t, Config{Backend: &countingBackend{}}) // MaxConcurrentSessions == 0, slots nil
	for i := 0; i < 1000; i++ {
		if !s.acquireSlot() {
			t.Fatal("unlimited server must always grant slots")
		}
	}
	s.releaseSlot() // must be a no-op, not panic
}
