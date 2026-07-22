//go:build wasip1

package sdk

import (
	"runtime"

	"github.com/Ceinl/plumtree/sdk/abi"
)

//go:wasmimport plumtree exec
func hostExec(reqPtr, reqLen, outPtr, outCap int32) int32

func execCommand(name string, args []string) (ExecResult, error) {
	if !validExecRequest(name, args) {
		return ExecResult{}, ErrExecTooLarge
	}
	req := abi.EncodeExecRequest(abi.ExecRequest{Name: name, Args: args})
	buf := make([]byte, 4096)
	for {
		n := hostExec(bytePtr(req), int32(len(req)), bytePtr(buf), int32(len(buf)))
		runtime.KeepAlive(req)
		switch {
		case n == abi.ExecErrUnavailable:
			return ExecResult{}, ErrExecUnavailable
		case n == abi.ExecErrTooLarge:
			return ExecResult{}, ErrExecTooLarge
		case n < 0:
			return ExecResult{}, ErrExecFailed
		case int(n) <= len(buf):
			resp, err := abi.DecodeExecResponse(buf[:n])
			runtime.KeepAlive(buf)
			if err != nil {
				return ExecResult{}, ErrExecFailed
			}
			return ExecResult{ExitCode: resp.ExitCode, Stdout: resp.Stdout, Stderr: resp.Stderr}, nil
		default:
			if n > 2*abi.ExecMaxOutput+12 {
				return ExecResult{}, ErrExecTooLarge
			}
			buf = make([]byte, n)
		}
	}
}

func validExecRequest(name string, args []string) bool {
	if name == "" || len(name) > abi.ExecMaxName || len(args) > abi.ExecMaxArgs {
		return false
	}
	for _, arg := range args {
		if len(arg) > abi.ExecMaxArg {
			return false
		}
	}
	return true
}
