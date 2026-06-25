package runner

import (
	"context"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/Ceinl/plumtree/sdk/abi"
)

// Env is the read-only secret/environment capability for a session. Values are
// server-side secrets, injected only for claimed apps; the guest can read them
// but never enumerate or write them. Like Auth it is per-session (the values are
// fixed for the session), not shared mutable state.
type Env interface {
	// Get returns the value for key. found is false when no secret is set.
	Get(key string) (value string, found bool)
}

// MapEnv is an Env backed by a fixed map, the common case (secrets are loaded
// once when the session starts).
type MapEnv map[string]string

func (e MapEnv) Get(key string) (string, bool) { v, ok := e[key]; return v, ok }

// registerEnv adds the env_get host function to b. Installed even when env is
// nil so a guest that kept the import can instantiate; calls then return
// abi.EnvErrInternal (distinct from EnvErrNotFound, so the guest can tell "no
// secrets capability" from "secret not set").
func registerEnv(b wazero.HostModuleBuilder, env Env) wazero.HostModuleBuilder {
	return b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, keyPtr, keyLen, outPtr, outCap int32) int32 {
			if env == nil {
				return abi.EnvErrInternal
			}
			if keyLen <= 0 || keyLen > abi.EnvMaxKey {
				return abi.EnvErrTooLarge
			}
			raw, ok := m.Memory().Read(uint32(keyPtr), uint32(keyLen))
			if !ok {
				return abi.EnvErrInternal
			}
			val, found := env.Get(string(raw))
			if !found {
				return abi.EnvErrNotFound
			}
			b := []byte(val)
			n := int32(len(b))
			if n > outCap {
				return n // report needed length; guest grows and retries
			}
			if n > 0 && !m.Memory().Write(uint32(outPtr), b) {
				return abi.EnvErrInternal
			}
			return n
		}).
		Export("env_get")
}
