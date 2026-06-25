package runner

import (
	"context"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/Ceinl/plumtree/sdk/abi"
)

// Identity is who is connected for a session. It mirrors abi.Identity and is
// supplied by the host (the SSH layer) per session, so unlike KV/Bus it is not
// shared across sessions.
type Identity struct {
	User          string // SSH key fingerprint, or an ephemeral per-session id
	Authenticated bool   // verified against a claimed owner key
}

// Auth is the per-session identity capability handed to a guest. Implementations
// return the identity of the connected user.
type Auth interface {
	Whoami() Identity
}

// StaticAuth is an Auth that always returns the same Identity — the common case,
// since a session's identity is fixed for its lifetime.
type StaticAuth struct{ Identity Identity }

func (a StaticAuth) Whoami() Identity { return a.Identity }

// registerAuth adds the auth_whoami host function to b. It is installed even
// when auth is nil so a guest whose linker kept the import can instantiate;
// calls then return abi.AuthErrInternal.
func registerAuth(b wazero.HostModuleBuilder, auth Auth) wazero.HostModuleBuilder {
	return b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, outPtr, outCap int32) int32 {
			if auth == nil {
				return abi.AuthErrInternal
			}
			id := auth.Whoami()
			enc := abi.EncodeIdentity(abi.Identity{User: id.User, Authenticated: id.Authenticated})
			n := int32(len(enc))
			// Too big for the guest buffer: report the needed length, write
			// nothing, let the guest grow and retry (mirrors kv_get).
			if n > outCap {
				return n
			}
			if !m.Memory().Write(uint32(outPtr), enc) {
				return abi.AuthErrInternal
			}
			return n
		}).
		Export("auth_whoami")
}
