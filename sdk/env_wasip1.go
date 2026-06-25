//go:build wasip1

package sdk

import (
	"runtime"

	"github.com/Ceinl/plumtree/sdk/abi"
)

//go:wasmimport plumtree env_get
func hostEnvGet(keyPtr, keyLen, outPtr, outCap int32) int32

func envGet(key string) (string, bool, error) {
	if len(key) == 0 || len(key) > abi.EnvMaxKey {
		return "", false, ErrEnvTooLarge
	}
	k := []byte(key)
	buf := make([]byte, 256)
	for {
		n := hostEnvGet(bytePtr(k), int32(len(k)), bytePtr(buf), int32(len(buf)))
		runtime.KeepAlive(k)
		switch {
		case n == abi.EnvErrNotFound:
			return "", false, nil
		case n == abi.EnvErrTooLarge:
			return "", false, ErrEnvTooLarge
		case n < 0:
			return "", false, ErrEnvUnavailable
		case int(n) <= len(buf):
			out := string(buf[:n])
			runtime.KeepAlive(buf)
			return out, true, nil
		default:
			buf = make([]byte, n)
		}
	}
}
