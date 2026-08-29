package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/runner"
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
	if status, ok := ch.exitStatus(); !ok || status != exitFailure {
		t.Fatalf("exit status = %d, present = %t; want %d", status, ok, exitFailure)
	}
}

func TestSuspensionDuringSessionStartSkipsGuest(t *testing.T) {
	backend := &blockingStartBackend{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	s := &Server{Backend: backend, Runner: runner.New()}
	s.sessions = newSessionRegistry()
	ch := &testChannel{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.startSession(ctx, cancel, ch, "alice/app", runner.Identity{}, nil, nil)
		close(done)
	}()

	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("StartSession was not called")
	}
	suspended := make(chan error, 1)
	go func() {
		suspended <- s.handleSuspension(context.Background(), Suspension{Scope: KillDeploy, ID: "deploy-1"})
	}()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("in-flight session was not cancelled")
	}
	close(backend.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session start did not finish")
	}
	if err := <-suspended; err != nil {
		t.Fatal(err)
	}
	if backend.recordCalls != 0 {
		t.Fatalf("guest ran and recorded %d logs", backend.recordCalls)
	}
	if backend.endCalls != 1 {
		t.Fatalf("EndSession calls = %d, want 1", backend.endCalls)
	}
	if status, ok := ch.exitStatus(); !ok || status != exitFailure {
		t.Fatalf("exit status = %d, present = %t; want %d", status, ok, exitFailure)
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

type blockingStartBackend struct {
	started     chan struct{}
	release     chan struct{}
	recordCalls int
	endCalls    int
}

func (*blockingStartBackend) ResolveIdentity(fingerprint string) (runner.Identity, error) {
	return runner.Identity{User: fingerprint}, nil
}
func (*blockingStartBackend) ResolveRunnable(string) (Runnable, error) {
	return Runnable{AppID: "app-1", AppName: "app", OwnerID: "owner-1", DeployID: "deploy-1", ArtifactDigest: "digest-1", AppType: "cli", WASM: []byte("must not run")}, nil
}
func (b *blockingStartBackend) StartSession(string, string) (string, error) {
	close(b.started)
	<-b.release
	return "session-1", nil
}
func (b *blockingStartBackend) RecordSessionLog(string, string, bool) error {
	b.recordCalls++
	return nil
}
func (b *blockingStartBackend) EndSession(string) error {
	b.endCalls++
	return nil
}
func (*blockingStartBackend) SecretsForApp(string) map[string]string { return nil }
func (*blockingStartBackend) EgressAllowlist(string) []string        { return nil }

func TestAcquireSlotUnlimited(t *testing.T) {
	s := &Server{} // MaxConcurrentSessions == 0, slots nil
	for i := 0; i < 1000; i++ {
		if !s.acquireSlot() {
			t.Fatal("unlimited server must always grant slots")
		}
	}
	s.releaseSlot() // must be a no-op, not panic
}
