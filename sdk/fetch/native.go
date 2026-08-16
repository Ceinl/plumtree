//go:build !wasip1

package fetch

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func fetch(ctx context.Context, method, url string, body []byte) (Response, error) {
	if len(url) == 0 || len(url) > abi.FetchMaxURL || len(body) > abi.FetchMaxBody {
		return Response{}, ErrTooLarge
	}
	if method == "" {
		method = http.MethodGet
	}
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return Response{}, ErrFailed
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Response{}, ctxErr
		}
		return Response{}, ErrFailed
	}
	defer response.Body.Close()
	output, err := io.ReadAll(io.LimitReader(response.Body, abi.FetchMaxBody+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Response{}, ctxErr
		}
		return Response{}, ErrFailed
	}
	if len(output) > abi.FetchMaxBody {
		return Response{}, ErrTooLarge
	}
	return Response{Status: response.StatusCode, Body: output}, nil
}
