package gateway

import (
	"path/filepath"

	"github.com/Ceinl/plumtree/runner"
)

// capsFor builds the capability set for an app's sessions: a per-app KV store
// (persisted, shared) and a per-app pub/sub bus (in-memory, shared across the
// live sessions of this process). Both are keyed by app ID so concurrent
// sessions of the same app see one instance. Secrets and egress are claimed-only:
// only apps with an owner get Env and a Fetcher, and egress stays default-deny
// unless the allowlist is non-empty.
func (s *Server) capsFor(appID, ownerID string) runner.Capabilities {
	if appID == "" {
		return runner.Capabilities{}
	}
	caps := runner.Capabilities{KV: s.kvFor(appID), Bus: s.busFor(appID)}
	if ownerID != "" {
		if secrets := s.Backend.SecretsForApp(appID); len(secrets) > 0 {
			caps.Env = runner.MapEnv(secrets)
		}
		if allow := s.Backend.EgressAllowlist(appID); len(allow) > 0 {
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
