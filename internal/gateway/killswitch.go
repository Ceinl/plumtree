package gateway

import (
	"context"
	"strconv"
	"sync"
)

// KillScope selects which live sessions a kill targets.
type KillScope int

const (
	KillApp    KillScope = iota // sessions of one app
	KillOwner                   // sessions of all of an owner's apps
	KillDeploy                  // sessions of one deploy
)

// sessionEntry is a live runner session the gateway can terminate.
type sessionEntry struct {
	ownerID  string
	appID    string
	deployID string
	cancel   context.CancelFunc
	done     chan struct{}
	invalid  bool
}

// sessionRegistry tracks live sessions so an operator kill switch can terminate
// them by app, owner, deploy, or all at once (the runner-wide switch). It is
// safe for concurrent use.
type sessionRegistry struct {
	mu           sync.Mutex
	sessions     map[string]*sessionEntry // keyed by session ID
	nextStarting uint64
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{sessions: make(map[string]*sessionEntry)}
}

func (r *sessionRegistry) add(sessionID string, e sessionEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.done == nil {
		e.done = make(chan struct{})
	}
	r.sessions[sessionID] = &e
}

// addStarting registers a resolved session before accounting begins. This
// closes the window where a suspension could be acknowledged while
// StartSession was still in flight.
func (r *sessionRegistry) addStarting(e sessionEntry) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.done == nil {
		e.done = make(chan struct{})
	}
	r.nextStarting++
	id := "starting-" + strconv.FormatUint(r.nextStarting, 10)
	r.sessions[id] = &e
	return id
}

// promote replaces the temporary accounting key with the durable session ID.
// It reports false when a suspension matched the session while accounting was
// in flight, so the caller can end the record without running the guest.
func (r *sessionRegistry) promote(startingID, sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[startingID]
	if !ok {
		return false
	}
	delete(r.sessions, startingID)
	r.sessions[sessionID] = e
	return !e.invalid
}

func (r *sessionRegistry) remove(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.sessions[sessionID]; ok {
		close(e.done)
	}
	delete(r.sessions, sessionID)
}

// kill cancels every live session matching scope/id and returns how many were
// cancelled. Cancelling is idempotent; the session goroutine deregisters itself
// as it unwinds.
func (r *sessionRegistry) kill(scope KillScope, id string) int {
	entries := r.matching(scope, id)
	for _, e := range entries {
		e.cancel()
	}
	return len(entries)
}

// killAndWait cancels matching sessions and acknowledges only after their
// goroutines deregister. This makes a successful suspension a hard boundary:
// no invalidated guest remains running when the caller resumes.
func (r *sessionRegistry) killAndWait(ctx context.Context, scope KillScope, id string) (int, error) {
	entries := r.matching(scope, id)
	for _, e := range entries {
		e.cancel()
	}
	for _, e := range entries {
		select {
		case <-e.done:
		case <-ctx.Done():
			return len(entries), ctx.Err()
		}
	}
	return len(entries), nil
}

func (r *sessionRegistry) matching(scope KillScope, id string) []*sessionEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	var entries []*sessionEntry
	for _, e := range r.sessions {
		var match bool
		switch scope {
		case KillApp:
			match = e.appID == id
		case KillOwner:
			match = e.ownerID == id
		case KillDeploy:
			match = e.deployID == id
		}
		if match {
			e.invalid = true
			entries = append(entries, e)
		}
	}
	return entries
}

// killAll cancels every live session (the runner-wide kill switch).
func (r *sessionRegistry) killAll() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.sessions {
		e.invalid = true
		e.cancel()
	}
	return len(r.sessions)
}
