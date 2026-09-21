// Package fetch provides typed gated outbound HTTP operations. Authority is
// the owner-enabled app's egress allowlist; a response lives only in its finite
// result. Native calls use the developer-friendly local adapter, while hosted
// isolation uses the selected host adapter and reports denial or absence as
// stable package errors.
package fetch

import (
	"context"
	"errors"
	"net/http"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/internal/operation"
)

var (
	ErrDenied      = errors.New("fetch: egress denied")
	ErrUnavailable = errors.New("fetch: capability unavailable")
	ErrTooLarge    = errors.New("fetch: request or response too large")
	ErrFailed      = errors.New("fetch: request failed")
	ErrInvalid     = errors.New("fetch: invalid request")
)

// Response is the bounded HTTP response returned by a request.
type Response struct {
	Status int
	Body   []byte
}

// Result is the complete typed result of a request.
type Result struct {
	Response
	Err error
}

// RequestOperation is an inert finite HTTP request.
type RequestOperation struct{ inner operation.Operation[Result] }

func (op RequestOperation) Run(ctx context.Context) Result { return op.inner.Run(ctx) }
func (op RequestOperation) Map(mapper func(Result) app.Event) app.Command {
	return op.inner.Map(mapper)
}
func (op RequestOperation) Ignore() app.Command { return op.inner.Ignore() }

// Request creates a typed HTTP operation. The body is copied before the
// operation is returned, so later caller mutation cannot alter the request.
func Request(method, url string, body []byte) RequestOperation {
	method = normalizeMethod(method)
	bodyCopy := append([]byte(nil), body...)
	return RequestOperation{inner: operation.New(func(ctx context.Context) Result {
		if err := validate(method, url, bodyCopy); err != nil {
			return Result{Err: err}
		}
		if err := ctx.Err(); err != nil {
			return Result{Err: err}
		}
		response, err := fetch(ctx, method, url, bodyCopy)
		return Result{Response: Response{Status: response.Status, Body: append([]byte(nil), response.Body...)}, Err: normalize(err)}
	})}
}

// Get creates a bounded GET operation.
func Get(url string) RequestOperation { return Request(http.MethodGet, url, nil) }

func normalizeMethod(method string) string {
	if method == "" {
		return http.MethodGet
	}
	return method
}

func validate(method, url string, body []byte) error {
	if method == "" || len(method) > abi.FetchMaxMethod || len(url) == 0 {
		return ErrInvalid
	}
	if len(url) > abi.FetchMaxURL || len(body) > abi.FetchMaxBody {
		return ErrTooLarge
	}
	return nil
}

func normalize(err error) error {
	if err == nil {
		return nil
	}
	return err
}
