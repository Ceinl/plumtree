//go:build wasip1

package hostexec

import (
	"runtime"
	"unsafe"

	"github.com/Ceinl/plumtree/sdk/abi"
)

//go:wasmimport plumtree exec
func hostExec(reqPtr, reqLen, outPtr, outCap int32) int32

func bytePtr(value []byte) int32 {
	if len(value) == 0 {
		return 0
	}
	return int32(uintptr(unsafe.Pointer(&value[0])))
}

func execCommand(name string, args []string) (Result, error) {
	if !valid(name, args) {
		return Result{}, ErrTooLarge
	}
	request := abi.EncodeExecRequest(abi.ExecRequest{Name: name, Args: args})
	buffer := make([]byte, 4096)
	for {
		n := hostExec(bytePtr(request), int32(len(request)), bytePtr(buffer), int32(len(buffer)))
		runtime.KeepAlive(request)
		switch {
		case n == abi.ExecErrUnavailable:
			return Result{}, ErrUnavailable
		case n == abi.ExecErrTooLarge:
			return Result{}, ErrTooLarge
		case n < 0:
			return Result{}, ErrFailed
		case int(n) <= len(buffer):
			response, err := abi.DecodeExecResponse(buffer[:n])
			if err != nil {
				return Result{}, ErrFailed
			}
			return Result{ExitCode: response.ExitCode, Stdout: response.Stdout, Stderr: response.Stderr}, nil
		default:
			if n > 2*abi.ExecMaxOutput+12 {
				return Result{}, ErrTooLarge
			}
			buffer = make([]byte, n)
		}
	}
}
