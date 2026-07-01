package runner

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// ErrEgressDenied reports that a Fetch target is not permitted: either the host
// is not on the app's allowlist (default-deny egress), or it resolved to a
// non-public IP (loopback/private/link-local/cloud-metadata), which is blocked
// to stop apps from using egress to reach the platform's own infrastructure
// (SSRF). The host maps it to abi.FetchErrDenied.
var ErrEgressDenied = errors.New("fetch: egress denied")

// errTooManyRedirects caps redirect chains so a hostile target cannot string the
// fetcher along indefinitely.
var errTooManyRedirects = errors.New("fetch: too many redirects")

// Fetcher is the gated outbound-HTTP capability. Egress is default-deny: an app
// with no Fetcher (or an empty allowlist) reaches nothing. Only claimed apps get
// a Fetcher, and only to allowlisted hosts.
type Fetcher interface {
	// Fetch performs req and returns the response, or ErrEgressDenied when the
	// target host is not permitted.
	Fetch(ctx context.Context, req abi.FetchRequest) (abi.FetchResponse, error)
}

// AllowlistFetcher dispatches requests only to hosts on Allow. A host matches if
// it equals an allow entry or is a subdomain of one (".example.com" covers
// "api.example.com"). An empty Allow denies everything — the default-deny
// posture.
//
// Beyond the name allowlist, the fetcher refuses to connect to non-public IPs on
// every dial — including each redirect hop and after DNS resolution — so an
// allowlisted name that resolves (or redirects) to a loopback/private/
// link-local/metadata address cannot be used to reach internal services. This
// closes DNS-rebinding and redirect-based SSRF.
type AllowlistFetcher struct {
	Allow []string
	// Client, when set, is used as-is (tests inject one). When nil, Fetch builds
	// a guarded client that enforces the IP policy.
	Client *http.Client
	// AllowPrivateIPs disables the non-public-IP block. It exists for tests and
	// self-host loopback setups; production must leave it false.
	AllowPrivateIPs bool
}

// NewAllowlistFetcher returns a fetcher permitting the given hosts, with a
// 10-second request timeout and the non-public-IP block enabled.
func NewAllowlistFetcher(allow []string) *AllowlistFetcher {
	f := &AllowlistFetcher{Allow: allow}
	f.Client = f.guardedClient()
	return f
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
		client = f.guardedClient()
	}
	resp, err := client.Do(hreq)
	if err != nil {
		// Surface the policy errors unwrapped so the guest sees "denied" rather
		// than a generic transport error.
		switch {
		case errors.Is(err, ErrEgressDenied):
			return abi.FetchResponse{}, ErrEgressDenied
		case errors.Is(err, errTooManyRedirects):
			return abi.FetchResponse{}, errTooManyRedirects
		}
		return abi.FetchResponse{}, err
	}
	defer resp.Body.Close()
	// Read one byte past the cap so an exactly-cap body still reads fully while an
	// oversized one is detected rather than silently truncated by LimitReader.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, abi.FetchMaxBody+1))
	if err != nil {
		return abi.FetchResponse{}, err
	}
	if len(respBody) > abi.FetchMaxBody {
		return abi.FetchResponse{}, errResponseTooLarge
	}
	return abi.FetchResponse{Status: resp.StatusCode, Body: respBody}, nil
}

var errBadRequest = errors.New("fetch: bad request")

// errResponseTooLarge reports that the response body exceeded abi.FetchMaxBody.
// The host maps it to abi.FetchErrTooLarge so the guest sees an explicit error
// instead of a truncated body.
var errResponseTooLarge = errors.New("fetch: response too large")

// guardedClient builds an http.Client that, on every connection (including
// redirect hops), rejects dials to non-public IPs and re-checks each redirect
// target against the allowlist. The dial guard runs after DNS resolution on the
// actual IP being connected to, which is what defeats DNS rebinding.
func (f *AllowlistFetcher) guardedClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			return f.checkDialAddr(address)
		},
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errTooManyRedirects
			}
			if !f.allowed(req.URL.Hostname()) {
				return ErrEgressDenied
			}
			return nil
		},
	}
}

// checkDialAddr blocks connecting to non-public IPs. address is "ip:port" with
// the host already resolved to an IP literal by the dialer.
func (f *AllowlistFetcher) checkDialAddr(address string) error {
	if f.AllowPrivateIPs {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// The dial Control callback is always handed a resolved IP; anything else
		// is unexpected, so fail closed.
		return ErrEgressDenied
	}
	if isNonPublicIP(ip) {
		return ErrEgressDenied
	}
	return nil
}

// isNonPublicIP reports whether ip is one an app must never reach via egress:
// loopback, RFC1918/ULA private, link-local (covers 169.254.169.254 cloud
// metadata), unspecified, or multicast.
func isNonPublicIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

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
				switch {
				case errors.Is(err, ErrEgressDenied):
					return abi.FetchErrDenied
				case errors.Is(err, errEgressUnavailable):
					return abi.FetchErrUnavail
				case errors.Is(err, errResponseTooLarge):
					return abi.FetchErrTooLarge
				default:
					return abi.FetchErrInternal
				}
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
