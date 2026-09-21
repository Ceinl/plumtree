package cleanrole

import (
	"bytes"
	"context"
	"sync"
	"syscall"
	"testing"
	"time"

	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
)

type stubComponent struct{}

func (stubComponent) Start(context.Context) error { return nil }
func (stubComponent) Ready(context.Context) error { return nil }
func (stubComponent) Stop(context.Context) error  { return nil }

func TestRunLifecycleReportsStoppedAfterCleanCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out lockedBuffer
	done := make(chan error, 1)
	go func() { done <- runLifecycle(ctx, serverconfig.Default(), stubComponent{}, &out, true) }()
	time.Sleep(20 * time.Millisecond) // let the notifier register first
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle did not stop")
	}
	if got := out.String(); got != "plumtree stopped\n" {
		t.Fatalf("lifecycle output = %q", got)
	}
}

func TestRunLifecycleKeepsRedirectedOutputClean(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out lockedBuffer
	done := make(chan error, 1)
	go func() { done <- runLifecycle(ctx, serverconfig.Default(), stubComponent{}, &out, false) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle did not stop")
	}
	if got := out.String(); got != "" {
		t.Fatalf("non-interactive output = %q, want silence", got)
	}
}

func TestStopNotifierPrintsOnTerminationSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out lockedBuffer
	lifecycleStopNotifier(ctx, &out)
	time.Sleep(20 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := out.String(); got != "plumtree stopping\n" {
		t.Fatalf("stopping output = %q", got)
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
