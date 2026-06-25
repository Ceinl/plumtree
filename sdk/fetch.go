package sdk

import "errors"

// Gated outbound HTTP — the claimed-and-allowlisted capability. Egress is
// default-deny: an app reaches the network only after it is claimed and the
// target host is on its egress allowlist (`pt egress add HOST`). Hosted apps
// call through the host; natively (`go run .`) Fetch uses the process's network
// directly so authors can develop against real endpoints.

var (
	// ErrEgressDenied means egress to the target is not permitted (the app is
	// unclaimed or the host is not on its allowlist).
	ErrEgressDenied = errors.New("sdk: egress denied")
	// ErrFetchUnavailable means the running context provides no fetch capability.
	ErrFetchUnavailable = errors.New("sdk: fetch capability unavailable")
	// ErrFetchTooLarge means the URL or body exceeds the host size limit.
	ErrFetchTooLarge = errors.New("sdk: fetch url or body too large")
	// ErrFetchFailed means the request could not be completed (DNS, connection,
	// timeout, or a malformed request).
	ErrFetchFailed = errors.New("sdk: fetch failed")
)

// Response is the result of a Fetch: an HTTP status code and the response body.
type Response struct {
	Status int
	Body   []byte
}

// Fetch performs an outbound HTTP request. method defaults to GET when empty;
// body may be nil. It returns ErrEgressDenied when the target is not allowlisted.
func Fetch(method, url string, body []byte) (Response, error) { return fetch(method, url, body) }

// Get is a convenience wrapper for a GET request.
func Get(url string) (Response, error) { return fetch("GET", url, nil) }
