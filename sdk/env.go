package sdk

import "errors"

// Server-side secrets — the claimed-only capability. An author sets a secret
// with `pt secret set KEY`; the platform stores it server-side and injects it
// at run time. The guest reads it with Env; it is never present at build time
// and unclaimed apps have no secrets.
//
// Natively (`go run .`) Env falls back to the process environment, so an author
// can develop against the same keys with real os env vars.

var (
	// ErrEnvUnavailable means the running context provides no env capability
	// (an unclaimed app, or a host failure).
	ErrEnvUnavailable = errors.New("sdk: env capability unavailable")
	// ErrEnvTooLarge means the key exceeds the host size limit.
	ErrEnvTooLarge = errors.New("sdk: env key too large")
)

// Env returns the value of the secret named key. ok is false when no such
// secret is set. Errors report a missing or failed capability.
func Env(key string) (value string, ok bool, err error) { return envGet(key) }
