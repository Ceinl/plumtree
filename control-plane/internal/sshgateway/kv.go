package sshgateway

import (
	"path/filepath"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/runner"
)

// capsFor builds the capability set for an app's sessions: a per-app KV store
// (persisted, shared) and a per-app pub/sub bus (in-memory, shared across the
// live sessions of this process). Both are keyed by app ID so concurrent
// sessions of the same app see one instance.
func (s *Server) capsFor(app control.App) runner.Capabilities {
	if app.ID == "" {
		return runner.Capabilities{}
	}
	caps := runner.Capabilities{KV: s.kvFor(app.ID), Bus: s.busFor(app.ID)}
	// Secrets and egress are claimed-only: only apps with an owner get Env and a
	// Fetcher, and egress stays default-deny unless the allowlist is non-empty.
	if app.OwnerID != "" {
		if secrets := s.Store.SecretsForApp(app.ID); len(secrets) > 0 {
			caps.Env = runner.MapEnv(secrets)
		}
		if allow := s.Store.EgressAllowlist(app.ID); len(allow) > 0 {
			caps.Fetch = runner.NewAllowlistFetcher(allow)
		}
	}
	return caps
}

func (s *Server) kvFor(appID string) runner.Store {
	if s.StateDir == "" {
		return nil
	}
	s.kvMu.Lock()
	defer s.kvMu.Unlock()
	if s.kvStores == nil {
		s.kvStores = make(map[string]runner.Store)
	}
	if st, ok := s.kvStores[appID]; ok {
		return st
	}
	path := filepath.Join(s.StateDir, "kv", appID+".json")
	st, err := runner.NewFileStore(path, runner.DefaultMaxKeys, runner.DefaultMaxBytes)
	if err != nil {
		s.logf("kv store for %q unavailable: %v", appID, err)
		return nil
	}
	s.kvStores[appID] = st
	return st
}

func (s *Server) busFor(appID string) runner.Bus {
	s.busMu.Lock()
	defer s.busMu.Unlock()
	if s.busById == nil {
		s.busById = make(map[string]*runner.MemBus)
	}
	if b, ok := s.busById[appID]; ok {
		return b
	}
	b := runner.NewMemBus()
	s.busById[appID] = b
	return b
}
