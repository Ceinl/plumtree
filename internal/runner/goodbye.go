package runner

import (
	"context"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// goodbye_set is the host function for sdk.GoodbyeSet. It reads a string from
// guest linear memory and stores it in the goodbye pointer on Capabilities.
// A nil goodbye pointer (caller opted out) silently ignores the write.
func registerGoodbye(b wazero.HostModuleBuilder, goodbye *string) wazero.HostModuleBuilder {
	return b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, ptr, length int32) {
			if goodbye == nil || length <= 0 || length > abi.GoodbyeMaxLen {
				return
			}
			raw, ok := m.Memory().Read(uint32(ptr), uint32(length))
			if !ok {
				return
			}
			*goodbye = string(raw)
		}).
		Export("goodbye_set")
}
