//go:build !wasip1

package secrets

import (
	"os"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func envGet(key string) (string, bool, error) {
	if len(key) == 0 || len(key) > abi.EnvMaxKey {
		return "", false, ErrTooLarge
	}
	value, found := os.LookupEnv(key)
	return value, found, nil
}
