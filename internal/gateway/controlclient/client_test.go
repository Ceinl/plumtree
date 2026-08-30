package controlclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/gateway"
)

// waitFor polls until cond passes or the deadline expires.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met within the deadline")
}

// A hostile or compromised control plane must not be able to grant owner
// authority: ownerID on an unauthenticated identity reply is dropped.
func TestResolveIdentityDropsOwnerWhenUnauthenticated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"user": "fp", "authenticated": false, "ownerID": "own_1"})
	}))
	defer srv.Close()

	id, err := New(srv.URL, "token").ResolveIdentity("fp")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Authenticated {
		t.Fatal("unauthenticated reply became authenticated")
	}
	if id.OwnerID != "" {
		t.Fatalf("unauthenticated identity carried owner authority: %+v", id)
	}
}

func TestResolveIdentityKeepsOwnerWhenAuthenticated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"user": "fp", "authenticated": true, "ownerID": "own_1"})
	}))
	defer srv.Close()

	id, err := New(srv.URL, "token").ResolveIdentity("fp")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if !id.Authenticated || id.OwnerID != "own_1" {
		t.Fatalf("identity = %+v, want authenticated with own_1", id)
	}
}

func TestSecretsAndEgressFailClosedWhenControlPlaneErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	if _, err := c.SecretsForApp("app-1"); !errors.Is(err, gateway.ErrCapsUnavailable) {
		t.Fatalf("SecretsForApp err = %v, want ErrCapsUnavailable", err)
	}
	if _, err := c.EgressAllowlist("app-1"); !errors.Is(err, gateway.ErrCapsUnavailable) {
		t.Fatalf("EgressAllowlist err = %v, want ErrCapsUnavailable", err)
	}
}

func TestSecretsAndEgressAbsenceIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/gateway/apps/app-1/secrets":
			_, _ = w.Write([]byte(`{}`))
		case "/internal/gateway/apps/app-1/egress":
			_, _ = w.Write([]byte(`{"allow":["example.com"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	secrets, err := c.SecretsForApp("app-1")
	if err != nil || secrets != nil {
		t.Fatalf("secrets = %v, %v; want nil with no error", secrets, err)
	}
	allow, err := c.EgressAllowlist("app-1")
	if err != nil || len(allow) != 1 || allow[0] != "example.com" {
		t.Fatalf("allow = %v, %v; want [example.com] with no error", allow, err)
	}
}

func TestEndSessionSendsIdempotencyKey(t *testing.T) {
	var key atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/gateway/sessions/session_1/end" {
			key.Store(r.Header.Get("Idempotency-Key"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := New(srv.URL, "token").EndSession("session_1"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if key.Load() != "end-session_1" {
		t.Fatalf("idempotency key = %v", key.Load())
	}
}

// A lost response must not leak the session's quota slot: the first attempt
// fails transiently and the background drain confirms the end with a replay
// carrying the same idempotency key.
func TestEndSessionRecoversSlotAfterLostResponse(t *testing.T) {
	var calls atomic.Int64
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if n == 1 {
			http.Error(w, "transient", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	c.endBackoff = time.Millisecond
	c.Logf = func(format string, args ...any) { t.Logf(format, args...) }
	if err := c.EndSession("session_1"); err == nil {
		t.Fatal("first attempt should report the transient failure")
	}
	waitFor(t, func() bool { return calls.Load() >= 2 })
	c.removeQueuedEnd("session_1")
	time.Sleep(20 * time.Millisecond)
	if got := int(calls.Load()); got != 2 {
		t.Fatalf("end-session called %d times after confirmation, want 2 (keys=%v)", got, keys)
	}
}

// A definitive 404 means the session no longer exists server-side; retrying
// would be pointless and must stop.
func TestEndSessionDefinitiveRejectionStopsRetrying(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unknown session", http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	c.endBackoff = time.Millisecond
	err := c.EndSession("session_1")
	var status *StatusError
	if !errors.As(err, &status) || status.Transient() {
		t.Fatalf("404 err = %v, want definitive StatusError", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := int(calls.Load()); got != 1 {
		t.Fatalf("definitive 404 retried %d extra times", got-1)
	}
}

// A persistent 503 is transient and keeps being retried.
func TestEndSessionTransientFailureKeepsRetrying(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	c.endBackoff = time.Millisecond
	if err := c.EndSession("session_1"); err == nil {
		t.Fatal("expected the transient failure to surface")
	}
	waitFor(t, func() bool { return calls.Load() >= 3 })
	c.removeQueuedEnd("session_1")
}

// A full retry queue degrades loudly without blocking session teardown.
func TestEndSessionQueueFullDegradesWithoutBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	c.endBackoff = time.Minute // keep every queued entry parked
	messages := make(chan string, 8)
	c.Logf = func(format string, args ...any) { messages <- fmt.Sprintf(format, args...) }
	for i := range maxPendingEndSessions {
		id := fmt.Sprintf("session_%d", i)
		if err := c.EndSession(id); err == nil {
			t.Fatal("expected the failure to surface")
		}
	}
	started := time.Now()
	if err := c.EndSession("overflow_session"); err == nil {
		t.Fatal("expected the failure to surface")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("queue-full EndSession blocked for %s", elapsed)
	}
	select {
	case msg := <-messages:
		if want := "retry queue is full"; !strings.Contains(msg, want) {
			t.Fatalf("degradation message %q missing %q", msg, want)
		}
	default:
		t.Fatal("queue-full degradation was not logged")
	}
	c.endMu.Lock()
	defer c.endMu.Unlock()
	if len(c.endQueue) != maxPendingEndSessions {
		t.Fatalf("queue length = %d, want %d", len(c.endQueue), maxPendingEndSessions)
	}
}

// A suspension delivery that times out or errors on next/poll is re-polled
// with growing delay instead of a fixed tight loop, and an unacked delivery
// keeps retrying its acknowledgement until the control plane confirms.
func TestSuspensionWatcherRetriesPollAndAckFailures(t *testing.T) {
	var nextCalls, ackCalls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/gateway/suspensions/next", func(w http.ResponseWriter, r *http.Request) {
		switch nextCalls.Add(1) {
		case 1:
			http.Error(w, "slow control plane", http.StatusGatewayTimeout)
		case 2:
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"deliveryID": "d-1", "scope": "deploy", "id": "dep-9"})
		}
	})
	mux.HandleFunc("/internal/gateway/suspensions/ack", func(w http.ResponseWriter, r *http.Request) {
		if ackCalls.Add(1) == 1 {
			http.Error(w, "ack lost", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/internal/gateway/suspensions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	handled := make(chan gateway.Suspension, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := New(srv.URL, "token").StartSuspensionWatcher(ctx, func(_ context.Context, event gateway.Suspension) error {
		handled <- event
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-handled:
		if event.Scope != gateway.KillDeploy || event.ID != "dep-9" {
			t.Fatalf("handled %+v, want deploy dep-9", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("suspension never delivered after poll failure recovery")
	}
	waitFor(t, func() bool { return ackCalls.Load() >= 2 })
	cancel()
}
