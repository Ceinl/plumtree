//go:build wasip1

package sdk

import (
	"runtime"

	"github.com/Ceinl/plumtree/sdk/abi"
)

//go:wasmimport plumtree auth_whoami
func hostAuthWhoami(outPtr, outCap int32) int32

func whoami() (Identity, error) {
	buf := make([]byte, 320) // fits flags + uint16 len + AuthMaxUser
	for {
		n := hostAuthWhoami(bytePtr(buf), int32(len(buf)))
		runtime.KeepAlive(buf)
		switch {
		case n == abi.AuthErrInternal:
			return Identity{}, ErrAuthUnavailable
		case n < 0:
			return Identity{}, ErrAuthUnavailable
		case int(n) <= len(buf):
			id, err := abi.DecodeIdentity(buf[:n])
			if err != nil {
				return Identity{}, ErrAuthUnavailable
			}
			return Identity{User: id.User, Authenticated: id.Authenticated}, nil
		default:
			buf = make([]byte, n)
		}
	}
}
