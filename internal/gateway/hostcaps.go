package gateway

import (
	"errors"
	"fmt"

	"github.com/Ceinl/plumtree/internal/runner"
)

// HostCapabilityOptions are the explicit operator settings that gate the
// high-authority hosted capabilities. They are copied out of Server at session
// start so hosted policy reads one value instead of reaching back into server
// state.
type HostCapabilityOptions struct {
	// EnableHostCommands gives claimed apps the ability to execute allowlisted
	// programs as the gateway OS user. Server.Start refuses enablement with an
	// empty allowlist; the assembler still denies unless both are set.
	EnableHostCommands bool
	// HostCommandAllowlist is the operator's executable allowlist consulted by
	// every host command.
	HostCommandAllowlist []string
}

// HostCapabilitySources are the required capability sources for one hosted
// session. Secrets and Egress come from the Backend (control plane), KV comes
// from the Backend only when it implements KVSource (nil means the backend has
// no durable store and the capability stays absent), and Bus provides the
// per-app shared pub/sub bus with reuse across the process.
type HostCapabilitySources struct {
	Secrets func(appID string) (map[string]string, error)
	Egress  func(appID string) ([]string, error)
	KV      func(appID string) (runner.Store, error)
	Bus     func(appID string) runner.Bus
}

// AssembleHostCapabilities builds the complete hosted capability set for one
// session from the resolved app, the app-relative identity, explicit operator
// options, and the required capability sources.
//
// The rules it owns:
//
//   - KV availability: nil KV source means no durable store (absent, no error);
//     a source error fails closed (absent plus an error).
//   - Bus reuse: one shared bus per app ID via the provided source.
//   - Claimed-only Env/Fetch/Exec: only apps with an owner ID get secrets,
//     egress, or host commands. Unclaimed apps get none, without an error.
//   - Default-deny fetch: empty allowlists wire no Fetcher; an invalid
//     allowlist stays default-deny and reports an error.
//   - Host-command rules: Exec requires a claimed app plus operator opt-in with
//     a non-empty allowlist.
//   - Goodbye allocation and Auth: every session gets a fresh Goodbye string
//     and a StaticAuth for the app-relative identity.
//
// Source failures are returned as a joined error so the caller can log them for
// the operator. They are never hidden as intentional absence: a nil error means
// every absent capability is intentional (unclaimed or unconfigured), a non-nil
// error means the returned set is degraded fail-closed and the session still
// runs with the absent capability.
func AssembleHostCapabilities(app Runnable, identity runner.Identity, opts HostCapabilityOptions, src HostCapabilitySources) (runner.Capabilities, error) {
	caps := runner.Capabilities{
		Auth:    runner.StaticAuth{Identity: identity},
		Goodbye: new(string),
	}
	if app.AppID == "" {
		return caps, errors.New("gateway: app ID is required for hosted capabilities")
	}
	var errs []error
	if src.Bus != nil {
		caps.Bus = src.Bus(app.AppID)
	}
	if src.KV != nil {
		store, err := src.KV(app.AppID)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("kv store for app %q unavailable; session runs without kv: %w", app.AppID, err))
		case store != nil:
			caps.KV = store
		}
	}
	// Secrets, egress, and host commands are claimed-only: only apps with an
	// owner get Env, a Fetcher, or Exec. Unclaimed preview apps get none.
	if app.OwnerID == "" {
		return caps, errors.Join(errs...)
	}
	if opts.EnableHostCommands && len(opts.HostCommandAllowlist) > 0 {
		caps.Exec = runner.LocalCommander{Allowlist: append([]string(nil), opts.HostCommandAllowlist...)}
	}
	if src.Secrets != nil {
		secrets, err := src.Secrets(app.AppID)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("secrets lookup for app %q failed; session runs without env: %w", app.AppID, err))
		case len(secrets) > 0:
			caps.Env = runner.MapEnv(secrets)
		}
	}
	if src.Egress != nil {
		allow, err := src.Egress(app.AppID)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("egress allowlist lookup for app %q failed; session runs default-deny: %w", app.AppID, err))
		case len(allow) > 0:
			fetcher, ferr := runner.NewValidatedAllowlistFetcher(allow)
			if ferr != nil {
				errs = append(errs, fmt.Errorf("egress allowlist for app %q rejected; session runs default-deny: %w", app.AppID, ferr))
				break
			}
			caps.Fetch = fetcher
		}
	}
	return caps, errors.Join(errs...)
}

// hostCapabilityOptions copies the operator-gated settings out of the server so
// session setup passes one explicit value into the hosted assembler.
func (s *Server) hostCapabilityOptions() HostCapabilityOptions {
	return HostCapabilityOptions{
		EnableHostCommands:   s.EnableHostCommands,
		HostCommandAllowlist: s.HostCommandAllowlist,
	}
}

// hostCapabilitySources wires the required capability sources out of the server:
// control-plane config from the Backend, durable KV only when the Backend
// exposes one, and the per-app shared bus with reuse across the process.
func (s *Server) hostCapabilitySources() HostCapabilitySources {
	src := HostCapabilitySources{Bus: s.busFor}
	if s.Backend == nil {
		return src
	}
	backend := s.Backend
	src.Secrets = backend.SecretsForApp
	src.Egress = backend.EgressAllowlist
	if kvSource, ok := backend.(KVSource); ok {
		src.KV = kvSource.KVStore
	}
	return src
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
