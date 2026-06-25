//go:build !wasip1

package sdk

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// Native fetch uses the process's network directly, so `go run .` can develop
// against real endpoints. The hosted build instead routes through the platform's
// default-deny egress allowlist.
func fetch(method, url string, body []byte) (Response, error) {
	if len(url) == 0 || len(url) > abi.FetchMaxURL || len(body) > abi.FetchMaxBody {
		return Response{}, ErrFetchTooLarge
	}
	if method == "" {
		method = http.MethodGet
	}
	var r io.Reader
	if len(body) > 0 {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return Response{}, ErrFetchFailed
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, ErrFetchFailed
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, abi.FetchMaxBody))
	if err != nil {
		return Response{}, ErrFetchFailed
	}
	return Response{Status: resp.StatusCode, Body: out}, nil
}
