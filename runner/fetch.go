package runner

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/Ceinl/plumtree/sdk/abi"
)

// ErrEgressDenied reports that a Fetch target is not permitted (default-deny
// egress: the host is not on the app's allowlist). The host maps it to
// abi.FetchErrDenied.
var ErrEgressDenied = errors.New("fetch: egress denied")

// Fetcher is the gated outbound-HTTP capability. Egress is default-deny: an app
// with no Fetcher (or an empty allowlist) reaches nothing. Only claimed apps get
// a Fetcher, and only to allowlisted hosts.
type Fetcher interface {
	// Fetch performs req and returns the response, or ErrEgressDenied when the
	// target host is not permitted.
	Fetch(ctx context.Context, req abi.FetchRequest) (abi.FetchResponse, error)
}

// AllowlistFetcher dispatches requests only to hosts on Allow, using Client (or
// a default client with a timeout). A host matches if it equals an allow entry
// or is a subdomain of one (".example.com" covers "api.example.com"). An empty
// Allow denies everything — the default-deny posture.
type AllowlistFetcher struct {
	Allow  []string
	Client *http.Client
}

// NewAllowlistFetcher returns a fetcher permitting the given hosts, with a
// 10-second request timeout.
func NewAllowlistFetcher(allow []string) *AllowlistFetcher {
	return &AllowlistFetcher{
		Allow:  allow,
		Client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (f *AllowlistFetcher) Fetch(ctx context.Context, req abi.FetchRequest) (abi.FetchResponse, error) {
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return abi.FetchResponse{}, errBadRequest
	}
	if !f.allowed(u.Hostname()) {
		return abi.FetchResponse{}, ErrEgressDenied
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = strings.NewReader(string(req.Body))
	}
	hreq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return abi.FetchResponse{}, errBadRequest
	}

	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return abi.FetchResponse{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, abi.FetchMaxBody))
	if err != nil {
		return abi.FetchResponse{}, err
	}
	return abi.FetchResponse{Status: resp.StatusCode, Body: respBody}, nil
}

var errBadRequest = errors.New("fetch: bad request")

// allowed reports whether host is permitted by the allowlist.
func (f *AllowlistFetcher) allowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range f.Allow {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if host == a || strings.HasSuffix(host, "."+strings.TrimPrefix(a, ".")) {
			return true
		}
	}
	return false
}

// registerFetch adds the fetch host function to b. Installed even when fetch is
// nil so a guest that kept the import can instantiate; calls then return
// abi.FetchErrUnavail.
func registerFetch(b wazero.HostModuleBuilder, fetch Fetcher) wazero.HostModuleBuilder {
	return b.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, reqPtr, reqLen, outPtr, outCap int32) int32 {
			if fetch == nil {
				return abi.FetchErrUnavail
			}
			if reqLen <= 0 {
				return abi.FetchErrInternal
			}
			raw, ok := m.Memory().Read(uint32(reqPtr), uint32(reqLen))
			if !ok {
				return abi.FetchErrInternal
			}
			req, err := abi.DecodeFetchRequest(raw)
			if err != nil {
				return abi.FetchErrInternal
			}
			if len(req.URL) > abi.FetchMaxURL || len(req.Body) > abi.FetchMaxBody {
				return abi.FetchErrTooLarge
			}
			resp, err := fetch.Fetch(ctx, req)
			if err != nil {
				if errors.Is(err, ErrEgressDenied) {
					return abi.FetchErrDenied
				}
				return abi.FetchErrInternal
			}
			enc := abi.EncodeFetchResponse(resp)
			n := int32(len(enc))
			if n > outCap {
				return n // report needed length; guest grows and retries
			}
			if !m.Memory().Write(uint32(outPtr), enc) {
				return abi.FetchErrInternal
			}
			return n
		}).
		Export("fetch")
}
