//go:build wasip1

package sdk

import (
	"runtime"

	"github.com/Ceinl/plumtree/sdk/abi"
)

//go:wasmimport plumtree fetch
func hostFetch(reqPtr, reqLen, outPtr, outCap int32) int32

func fetch(method, url string, body []byte) (Response, error) {
	if len(url) == 0 || len(url) > abi.FetchMaxURL || len(body) > abi.FetchMaxBody {
		return Response{}, ErrFetchTooLarge
	}
	enc := abi.EncodeFetchRequest(abi.FetchRequest{Method: method, URL: url, Body: body})
	buf := make([]byte, 4096)
	for {
		n := hostFetch(bytePtr(enc), int32(len(enc)), bytePtr(buf), int32(len(buf)))
		runtime.KeepAlive(enc)
		switch {
		case n == abi.FetchErrDenied:
			return Response{}, ErrEgressDenied
		case n == abi.FetchErrTooLarge:
			return Response{}, ErrFetchTooLarge
		case n == abi.FetchErrUnavail:
			return Response{}, ErrFetchUnavailable
		case n < 0:
			return Response{}, ErrFetchFailed
		case int(n) <= len(buf):
			resp, err := abi.DecodeFetchResponse(buf[:n])
			runtime.KeepAlive(buf)
			if err != nil {
				return Response{}, ErrFetchFailed
			}
			return Response{Status: resp.Status, Body: resp.Body}, nil
		default:
			buf = make([]byte, n)
		}
	}
}
