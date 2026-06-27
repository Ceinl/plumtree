package gateway

import "errors"

// Backend is the gateway's port to the control plane. The gateway owns SSH
// session lifecycle and the WASM sandbox; everything that requires authoritative
// platform state — resolving an app handle to runnable WASM, session accounting
// and quotas, and claimed-only capability config — is delegated through this
// interface. It is satisfied in-process by the control plane's store adapter and
// out-of-process by an HTTP client, so the same gateway runs embedded or as its
// own deployable.
type Backend interface {
	// ResolveRunnable maps an SSH user (the app handle) to its active deploy's
	// runnable WASM. It returns ErrSuspended for a suspended app and a generic
	// error when the handle does not resolve.
	ResolveRunnable(handle string) (Runnable, error)

	// StartSession opens a session for accounting and quota enforcement. The
	// returned id keys RecordSessionLog and EndSession. It returns ErrQuota when
	// the app has hit its connection limit.
	StartSession(appID, deployID string) (sessionID string, err error)

	// RecordSessionLog stores the guest's captured stdout/stderr for a session.
	RecordSessionLog(sessionID, log string, truncated bool) error

	// EndSession marks a session finished, releasing its accounting slot.
	EndSession(sessionID string) error

	// SecretsForApp returns the env/secret values injected into a claimed app's
	// sessions, or nil when the app has none (or is unclaimed).
	SecretsForApp(appID string) map[string]string

	// EgressAllowlist returns the fetch allowlist for a claimed app, or nil when
	// the app has none (or is unclaimed). Egress stays default-deny when empty.
	EgressAllowlist(appID string) []string
}

// Runnable is a resolved app ready to serve a session. WASM is the compiled
// guest module for the app's active deploy.
type Runnable struct {
	AppID    string
	AppName  string
	OwnerID  string
	DeployID string
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
)
