//go:build !wasip1

package sdk

import (
	"os"
	"strconv"
)

// Native whoami returns a fixed local identity so `go run .` and tests behave
// like a hosted session. There is no SSH layer locally, so the user is a stable
// placeholder rather than a key fingerprint.
func whoami() (Identity, error) {
	return Identity{
		User:          envDefault("PLUMTREE_IDENTITY_USER", "local"),
		Kind:          IdentityKind(envDefault("PLUMTREE_IDENTITY_KIND", string(IdentitySSHKey))),
		Authenticated: envBoolDefault("PLUMTREE_IDENTITY_AUTHENTICATED", true),
		OwnsApp:       envBoolDefault("PLUMTREE_IDENTITY_OWNS_APP", true),
	}, nil
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBoolDefault(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}
