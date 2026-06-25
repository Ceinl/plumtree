//go:build !wasip1

package sdk

import (
	"os"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// Native env reads the process environment, so `go run .` can exercise the same
// secret keys via real os env vars. The hosted build instead reads server-side
// secrets injected by the platform.
func envGet(key string) (string, bool, error) {
	if len(key) == 0 || len(key) > abi.EnvMaxKey {
		return "", false, ErrEnvTooLarge
	}
	v, ok := os.LookupEnv(key)
	return v, ok, nil
}
