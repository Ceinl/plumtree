package gateway

import (
	"github.com/Ceinl/plumtree/internal/runner"
)

// capsFor builds the capability set for an app's sessions: a per-app KV store
// (persisted, shared) and a per-app pub/sub bus (in-memory, shared across the
// live sessions of this process). Both are keyed by app ID so concurrent
// sessions of the same app see one instance. Secrets and egress are claimed-only:
// only apps with an owner get Env and a Fetcher, and egress stays default-deny
// unless the allowlist is non-empty. A capability source that cannot be read
// fails closed: the capability is left absent and the operator sees why.
func (s *Server) capsFor(appID, ownerID string) runner.Capabilities {
	if appID == "" {
		return runner.Capabilities{}
	}
	caps := runner.Capabilities{KV: s.kvFor(appID), Bus: s.busFor(appID), Goodbye: new(string)}
	if ownerID != "" {
		if s.enableHostCommands && len(s.hostCommandAllowlist) > 0 {
			caps.Exec = runner.LocalCommander{Allowlist: s.hostCommandAllowlist}
		}
		secrets, err := s.backend.SecretsForApp(appID)
		switch {
		case err != nil:
			s.logf("ERROR: secrets lookup for app %q failed; session runs without env: %v", appID, err)
		case len(secrets) > 0:
			caps.Env = runner.MapEnv(secrets)
		}
		allow, err := s.backend.EgressAllowlist(appID)
		switch {
		case err != nil:
			s.logf("ERROR: egress allowlist lookup for app %q failed; session runs default-deny: %v", appID, err)
		case len(allow) > 0:
			fetcher, ferr := runner.NewValidatedAllowlistFetcher(allow)
			if ferr != nil {
				s.logf("ERROR: egress allowlist for app %q rejected (%v); session runs default-deny", appID, ferr)
				break
			}
			caps.Fetch = fetcher
		}
	}
	return caps
}

// kvFor returns the app's durable KV store. Hosted sessions use the
// repository-backed store when the backend exposes one; there is no file-based
// fallback on this path (pt dev local profiles keep their own JSON store).
func (s *Server) kvFor(appID string) runner.Store {
	if source, ok := s.backend.(KVSource); ok {
		st, err := source.KVStore(appID)
		if err != nil {
			s.logf("ERROR: kv store for app %q unavailable; session runs without kv: %v", appID, err)
			return nil
		}
		return st
	}
	return nil
}

func (s *Server) busFor(appID string) runner.Bus {
	s.busMu.Lock()
	defer s.busMu.Unlock()
	if b, ok := s.busById[appID]; ok {
		return b
	}
	b := runner.NewMemBus()
	s.busById[appID] = b
	return b
}
