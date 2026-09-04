package gateway

import (
	"context"

	"github.com/Ceinl/plumtree/internal/runner"
)

// capsFor builds the capability set for an app's sessions: a per-app KV store
// (persisted, shared) and a per-app pub/sub bus (in-memory, shared across the
// live sessions of this process). Both are keyed by app ID so concurrent
// sessions of the same app see one instance. Secrets and egress are claimed-only:
// only apps with an owner get Env and a Fetcher, and egress stays default-deny
// unless the allowlist is non-empty. A capability source that cannot be read
// fails closed: the capability is left absent and the operator sees why.
func (s *Server) capsFor(ctx context.Context, appID, ownerID string) runner.Capabilities {
	if appID == "" {
		return runner.Capabilities{}
	}
	caps := runner.Capabilities{KV: s.kvFor(ctx, appID), Bus: s.busFor(appID), Goodbye: new(string)}
	if ownerID != "" {
		if s.EnableHostCommands && len(s.HostCommandAllowlist) > 0 {
			caps.Exec = runner.LocalCommander{Allowlist: s.HostCommandAllowlist}
		}
		secrets, err := s.Backend.SecretsForApp(ctx, appID)
		switch {
		case err != nil:
			s.logf("ERROR: secrets lookup for app %q failed; session runs without env: %v", appID, err)
		case len(secrets) > 0:
			caps.Env = runner.MapEnv(secrets)
		}
		allow, err := s.Backend.EgressAllowlist(ctx, appID)
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
// repository-backed store; there is no file-based fallback on this path
// (pt dev local profiles keep their own JSON store).
func (s *Server) kvFor(ctx context.Context, appID string) runner.Store {
	st, err := s.Backend.KVStore(ctx, appID)
	if err != nil {
		s.logf("ERROR: kv store for app %q unavailable; session runs without kv: %v", appID, err)
		return nil
	}
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
