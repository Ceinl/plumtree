package gateway

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/runner"
	"golang.org/x/crypto/ssh"
)

// mustNewServer constructs a Server through New, failing the test on error.
// Tests use the constructor and observable behavior instead of manually
// preparing slots or other private state.
func mustNewServer(t *testing.T, c Config) *Server {
	t.Helper()
	if c.Suspensions == nil {
		if source, ok := c.Backend.(SuspensionSource); ok {
			c.Suspensions = source
		}
	}
	s, err := New(c)
	if err != nil {
		t.Fatalf("New(%+v) = %v", c, err)
	}
	return s
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	backend := &stubBackend{}
	for _, tc := range []struct {
		name    string
		config  Config
		wantErr string
	}{
		{"missing backend", Config{}, "backend is required"},
		{"missing suspension source", Config{Backend: backend}, "suspension source is required"},
		{
			"endpoint and worker are mutually exclusive",
			Config{Backend: backend, Suspensions: backend, RunnerEndpoint: "unix:///run/plumtree.sock", RunnerWorker: "/bin/worker", RunnerToken: "tok"},
			"either runner endpoint or local runner worker",
		},
		{
			"endpoint requires a token",
			Config{Backend: backend, Suspensions: backend, RunnerEndpoint: "unix:///run/plumtree.sock"},
			"runner token is required",
		},
		{
			"host commands require an allowlist",
			Config{Backend: backend, Suspensions: backend, EnableHostCommands: true},
			"empty allowlist",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.config); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("New err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestNewInitializesOneValidState(t *testing.T) {
	s := mustNewServer(t, Config{Backend: &stubBackend{}})
	if s.runner == nil {
		t.Fatal("runner was not initialized")
	}
	if s.sessions == nil {
		t.Fatal("session registry was not initialized")
	}
	if s.admission == nil {
		t.Fatal("connection admission was not initialized")
	}
	if s.busById == nil {
		t.Fatal("bus state was not initialized")
	}
	if s.slots != nil {
		t.Fatal("zero MaxConcurrentSessions should leave capacity unlimited (nil slots)")
	}
	if s.handshakeTimeout != DefaultHandshakeTimeout {
		t.Fatalf("handshake timeout = %v, want default %v", s.handshakeTimeout, DefaultHandshakeTimeout)
	}
	if s.idleTimeout != DefaultIdleTimeout {
		t.Fatalf("idle timeout = %v, want default %v", s.idleTimeout, DefaultIdleTimeout)
	}
	if s.limits != runner.DefaultLimits {
		t.Fatalf("limits = %+v, want runner defaults %+v", s.limits, runner.DefaultLimits)
	}
	// Observable behavior of a fresh server: unlimited capacity grants slots,
	// admission accepts connections, and the per-app bus is shared.
	if !s.acquireSlot() {
		t.Fatal("fresh unlimited server refused a capacity slot")
	}
	s.releaseSlot()
	if !s.admission.Acquire("198.51.100.1") {
		t.Fatal("fresh server refused an admitted connection")
	}
	s.admission.Release("198.51.100.1")
	if a, b := s.busFor("app-1"), s.busFor("app-1"); a != b {
		t.Fatal("per-app bus was not shared across lookups")
	}
	if a, b := s.busFor("app-1"), s.busFor("app-2"); a == b {
		t.Fatal("bus leaked across app IDs")
	}
}

func TestNewResolvesZeroAndNegativeSentinels(t *testing.T) {
	s := mustNewServer(t, Config{
		Backend:               &stubBackend{},
		HandshakeTimeout:      -1, // disabled
		IdleTimeout:           -1, // disabled
		MaxConnections:        -1, // disabled
		MaxConnectionsPerIP:   -1, // disabled
		MaxConcurrentSessions: 3,
	})
	if s.handshakeTimeout != 0 {
		t.Fatalf("negative handshake timeout resolved to %v, want 0 (disabled)", s.handshakeTimeout)
	}
	if s.idleTimeout != 0 {
		t.Fatalf("negative idle timeout resolved to %v, want 0 (disabled)", s.idleTimeout)
	}
	// Disabled connection limits admit beyond the secure defaults: neither the
	// global cap nor the per-IP cap rejects.
	for i := 0; i < DefaultMaxConnections+1; i++ {
		if !s.admission.Acquire("192.0.2.1") {
			t.Fatalf("disabled admission rejected connection %d", i)
		}
	}
	if cap(s.slots) != 3 {
		t.Fatalf("slot capacity = %d, want 3", cap(s.slots))
	}
}

func TestNewCopiesHostCommandAllowlist(t *testing.T) {
	allow := []string{"echo"}
	s := mustNewServer(t, Config{Backend: &stubBackend{}, EnableHostCommands: true, HostCommandAllowlist: allow})
	allow[0] = "mutated"
	caps := s.capsFor(context.Background(), "app-1", "owner-1")
	cmd, ok := caps.Exec.(runner.LocalCommander)
	if !ok {
		t.Fatalf("exec capability = %T, want runner.LocalCommander", caps.Exec)
	}
	if len(cmd.Allowlist) != 1 || cmd.Allowlist[0] != "echo" {
		t.Fatalf("allowlist = %q, want [echo]; constructor must copy the slice", cmd.Allowlist)
	}
}

// watcherBackend records watcher registration so Start's only remaining job —
// context-bound suspension-watcher registration — is observable.
type watcherBackend struct {
	stubBackend
	mu            sync.Mutex
	registrations int
}

func (b *watcherBackend) StartSuspensionWatcher(_ context.Context, _ func(context.Context, Suspension) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.registrations++
	return nil
}

func (b *watcherBackend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.registrations
}

func TestStartRegistersSuspensionWatcherOnce(t *testing.T) {
	backend := &watcherBackend{}
	s := mustNewServer(t, Config{Backend: backend})
	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Start(ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if n := backend.count(); n != 1 {
		t.Fatalf("watcher registrations = %d, want 1 (Start is idempotent)", n)
	}
}

func TestStartPropagatesWatcherError(t *testing.T) {
	backend := &failingWatcherBackend{err: errors.New("watcher unavailable")}
	s := mustNewServer(t, Config{Backend: backend})
	if err := s.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "watcher unavailable") {
		t.Fatalf("Start err = %v, want watcher failure", err)
	}
}

type failingWatcherBackend struct {
	stubBackend
	err error
}

func (b *failingWatcherBackend) StartSuspensionWatcher(_ context.Context, _ func(context.Context, Suspension) error) error {
	return b.err
}

// Concurrent HandleSession calls must be race-clean: construction initialized
// everything, so session handling performs no initialization writes. Run with
// -race; the old lazy init of Runner/sessions/slots raced here.
func TestHandleSessionConcurrentIsRaceClean(t *testing.T) {
	backend := &stubBackend{resolveErr: errors.New("no such app")}
	s := mustNewServer(t, Config{Backend: backend, MaxConcurrentSessions: 4})
	const n = 16
	channels := make([]*testChannel, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		channels[i] = &testChannel{}
		reqs := make(chan *ssh.Request, 1)
		reqs <- &ssh.Request{Type: "shell"}
		close(reqs)
		wg.Add(1)
		go func(ch *testChannel, r <-chan *ssh.Request) {
			defer wg.Done()
			s.HandleSession(context.Background(), ch, r, "alice/tool", runner.Identity{})
		}(channels[i], reqs)
	}
	wg.Wait()
	// Every session was rejected at resolve time; each must report exactly one
	// nonzero exit status once its goroutine finishes.
	deadline := time.Now().Add(5 * time.Second)
	for i, ch := range channels {
		for {
			if got := ch.exitStatuses(); len(got) == 1 && got[0] != 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("session %d statuses = %v, want one nonzero status", i, ch.exitStatuses())
			}
			time.Sleep(time.Millisecond)
		}
	}
}
