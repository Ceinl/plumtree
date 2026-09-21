//go:build !wasip1

package identity

import (
	"os"
	"strconv"
)

func whoami() (Identity, error) {
	return Identity{
		User:          envDefault("PLUMTREE_IDENTITY_USER", "local"),
		Kind:          Kind(envDefault("PLUMTREE_IDENTITY_KIND", string(KindSSHKey))),
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
