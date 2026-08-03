//go:build wasip1

package identity

import (
	"runtime"
	"unsafe"

	"github.com/Ceinl/plumtree/sdk/abi"
)

//go:wasmimport plumtree auth_whoami
func hostAuthWhoami(outPtr, outCap int32) int32

func bytePtr(value []byte) int32 {
	if len(value) == 0 {
		return 0
	}
	return int32(uintptr(unsafe.Pointer(&value[0])))
}

func whoami() (Identity, error) {
	buffer := make([]byte, 320)
	for {
		n := hostAuthWhoami(bytePtr(buffer), int32(len(buffer)))
		runtime.KeepAlive(buffer)
		switch {
		case n == abi.AuthErrInternal || n < 0:
			return Identity{}, ErrUnavailable
		case int(n) <= len(buffer):
			value, err := abi.DecodeIdentity(buffer[:n])
			if err != nil {
				return Identity{}, ErrUnavailable
			}
			return Identity{User: value.User, Authenticated: value.Authenticated, Kind: identityKind(value.Kind), OwnsApp: value.OwnsApp}, nil
		default:
			buffer = make([]byte, n)
		}
	}
}

func identityKind(kind abi.IdentityKind) Kind {
	switch kind {
	case abi.IdentitySSHKey:
		return KindSSHKey
	case abi.IdentityAnonymous:
		return KindAnonymous
	default:
		return ""
	}
}
