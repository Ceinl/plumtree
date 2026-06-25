package sshgateway

import (
	"context"
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
}

// sessionRegistry tracks live sessions so an operator kill switch can terminate
// them by app, owner, deploy, or all at once (the runner-wide switch). It is
// safe for concurrent use.
type sessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]sessionEntry // keyed by session ID
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{sessions: make(map[string]sessionEntry)}
}

func (r *sessionRegistry) add(sessionID string, e sessionEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[sessionID] = e
}

func (r *sessionRegistry) remove(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
}

// kill cancels every live session matching scope/id and returns how many were
// cancelled. Cancelling is idempotent; the session goroutine deregisters itself
// as it unwinds.
func (r *sessionRegistry) kill(scope KillScope, id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
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
			e.cancel()
			n++
		}
	}
	return n
}

// killAll cancels every live session (the runner-wide kill switch).
func (r *sessionRegistry) killAll() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.sessions {
		e.cancel()
	}
	return len(r.sessions)
}
