//go:build wasip1

package secrets

import (
	"runtime"
	"unsafe"

	"github.com/Ceinl/plumtree/sdk/abi"
)

//go:wasmimport plumtree env_get
func hostEnvGet(keyPtr, keyLen, outPtr, outCap int32) int32

func bytePtr(value []byte) int32 {
	if len(value) == 0 {
		return 0
	}
	return int32(uintptr(unsafe.Pointer(&value[0])))
}

func envGet(key string) (string, bool, error) {
	if len(key) == 0 || len(key) > abi.EnvMaxKey {
		return "", false, ErrTooLarge
	}
	keyBytes := []byte(key)
	buffer := make([]byte, 256)
	for {
		n := hostEnvGet(bytePtr(keyBytes), int32(len(keyBytes)), bytePtr(buffer), int32(len(buffer)))
		runtime.KeepAlive(keyBytes)
		switch {
		case n == abi.EnvErrNotFound:
			return "", false, nil
		case n == abi.EnvErrTooLarge:
			return "", false, ErrTooLarge
		case n < 0:
			return "", false, ErrUnavailable
		case int(n) <= len(buffer):
			return string(buffer[:n]), true, nil
		default:
			buffer = make([]byte, n)
		}
	}
}
