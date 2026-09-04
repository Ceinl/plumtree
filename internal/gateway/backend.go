package gateway

import (
	"context"
	"errors"

	"github.com/Ceinl/plumtree/internal/runner"
)

// Backend is the gateway's port to the control plane. The gateway owns SSH
// session lifecycle and the WASM sandbox; everything that requires authoritative
// platform state — resolving an app handle to runnable WASM, session accounting
// and quotas, and claimed-only capability config — is delegated through this
// interface. It is satisfied in-process by the control plane's store adapter.
// There is one resolution path and one accounting path; optional behavior is
// not discovered with type assertions.
type Backend interface {
	// ResolveIdentity checks a proved SSH public-key fingerprint against the
	// control plane. Unknown fingerprints are returned as unauthenticated key
	// identities; only fingerprints registered to an owner are authenticated.
	ResolveIdentity(ctx context.Context, fingerprint string) (runner.Identity, error)

	// ResolveRunnable maps an SSH user (the app handle) to its active deploy's
	// runnable WASM, applying the proved leaf identity to restricted-app
	// authorization. It returns ErrSuspended for a suspended app and a generic
	// error when the handle does not resolve.
	ResolveRunnable(ctx context.Context, handle string, identity runner.Identity) (Runnable, error)

	// StartSession opens a session for accounting and quota enforcement,
	// recording the immutable artifact digest and app-relative identity summary
	// that actually entered the session. The returned id keys RecordSessionLog
	// and EndSession. It returns ErrQuota when the app has hit its connection
	// limit and ErrSuspended when the app was suspended before admission.
	StartSession(ctx context.Context, appID, deployID, artifactDigest, identitySummary string) (sessionID string, err error)

	// RecordSessionLog stores the guest's captured stdout/stderr for a session.
	RecordSessionLog(ctx context.Context, sessionID, log string, truncated bool) error

	// EndSession marks a session finished, releasing its accounting slot.
	EndSession(ctx context.Context, sessionID string) error

	// SecretsForApp returns the env/secret values injected into a claimed app's
	// sessions, or (nil, nil) when the app has none (or is unclaimed). An error
	// means the control plane could not answer; the gateway must fail closed.
	SecretsForApp(ctx context.Context, appID string) (map[string]string, error)

	// EgressAllowlist returns the fetch allowlist for a claimed app, or
	// (nil, nil) when the app has none (or is unclaimed). Egress stays
	// default-deny when empty. An error means the control plane could not
	// answer; the gateway must fail closed.
	EgressAllowlist(ctx context.Context, appID string) ([]string, error)

	// KVStore returns the shared runner.Store for one app's sessions.
	KVStore(ctx context.Context, appID string) (runner.Store, error)
}

// SuspensionSource streams administrative suspension events to a gateway.
// It is an explicit Server dependency, not optional behavior discovered on
// Backend. Handle must not return until every matching live session has
// stopped; its return is the gateway's acknowledgement.
type SuspensionSource interface {
	// StartSuspensionWatcher registers this gateway before returning, then keeps
	// consuming events until ctx ends. Registration-before-return ensures the
	// SSH listener never admits a session before the kill-switch path is live.
	StartSuspensionWatcher(ctx context.Context, handle func(context.Context, Suspension) error) error
}

// Suspension identifies the live sessions invalidated by an administrative
// owner, app, or deploy suspension.
type Suspension struct {
	Scope KillScope
	ID    string
}

// Runnable is a resolved app ready to serve a session. WASM is the compiled
// guest module for the app's active deploy.
type Runnable struct {
	AppID          string
	AppName        string
	OwnerID        string
	DeployID       string
	ArtifactDigest string
	// AppType is "tui" (default) or "cli"; it selects the runner entry point.
	AppType string
	WASM    []byte
}

// Sentinel errors a Backend reports so the gateway can render the right
// user-facing message. Backends wrap these (errors.Is matches) rather than
// returning them verbatim, so the underlying detail is preserved for logging.
var (
	// ErrSuspended means the app exists but is administratively suspended.
	ErrSuspended = errors.New("gateway: app suspended")
	// ErrQuota means the app has reached its connection/session limit.
	ErrQuota = errors.New("gateway: quota exceeded")
	// ErrCapsUnavailable means authoritative backend data could not be read (secrets,
	// egress allowlist, KV, identity, resolution, or accounting). Owner
	// capabilities must fail closed: an error here is never equivalent to "no
	// secrets configured".
	ErrCapsUnavailable = errors.New("gateway: control plane unavailable")
	// ErrNotConfigured means the gateway backend itself is missing required
	// configuration (nil repository, nil dependency). Unlike ErrCapsUnavailable,
	// which reports a reachable control plane that failed to answer, this
	// reports a backend that was never wired up.
	ErrNotConfigured = errors.New("gateway: backend is not configured")
)
