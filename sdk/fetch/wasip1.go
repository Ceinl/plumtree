//go:build wasip1

package fetch

import (
	"context"
	"runtime"
	"unsafe"

	"github.com/Ceinl/plumtree/sdk/abi"
)

//go:wasmimport plumtree fetch
func hostFetch(reqPtr, reqLen, outPtr, outCap int32) int32

func bytePtr(value []byte) int32 {
	if len(value) == 0 {
		return 0
	}
	return int32(uintptr(unsafe.Pointer(&value[0])))
}

func fetch(ctx context.Context, method, url string, body []byte) (Response, error) {
	if len(url) == 0 || len(url) > abi.FetchMaxURL || len(body) > abi.FetchMaxBody {
		return Response{}, ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	request := abi.EncodeFetchRequest(abi.FetchRequest{Method: method, URL: url, Body: body})
	buffer := make([]byte, 4096)
	for {
		n := hostFetch(bytePtr(request), int32(len(request)), bytePtr(buffer), int32(len(buffer)))
		runtime.KeepAlive(request)
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		switch {
		case n == abi.FetchErrDenied:
			return Response{}, ErrDenied
		case n == abi.FetchErrTooLarge:
			return Response{}, ErrTooLarge
		case n == abi.FetchErrUnavail:
			return Response{}, ErrUnavailable
		case n < 0:
			return Response{}, ErrFailed
		case int(n) <= len(buffer):
			response, err := abi.DecodeFetchResponse(buffer[:n])
			if err != nil {
				return Response{}, ErrFailed
			}
			return Response{Status: response.Status, Body: response.Body}, nil
		default:
			buffer = make([]byte, n)
		}
	}
}
