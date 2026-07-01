package runner

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestAllowlistFetcherAllowsAndDenies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("brewed"))
	}))
	defer srv.Close()

	// Allowlisted host: request goes through. httptest serves on loopback, so
	// permit private IPs for this test (production leaves AllowPrivateIPs false).
	f := NewAllowlistFetcher([]string{"127.0.0.1"})
	f.AllowPrivateIPs = true
	resp, err := f.Fetch(context.Background(), abi.FetchRequest{URL: srv.URL})
	if err != nil {
		t.Fatalf("allowed fetch: %v", err)
	}
	if resp.Status != http.StatusTeapot || string(resp.Body) != "brewed" {
		t.Fatalf("resp = %d %q", resp.Status, resp.Body)
	}

	// Empty allowlist: default-deny.
	deny := NewAllowlistFetcher(nil)
	if _, err := deny.Fetch(context.Background(), abi.FetchRequest{URL: srv.URL}); err != ErrEgressDenied {
		t.Fatalf("deny fetch err = %v, want ErrEgressDenied", err)
	}

	// Non-allowlisted host: denied even with a non-empty allowlist.
	other := NewAllowlistFetcher([]string{"example.com"})
	if _, err := other.Fetch(context.Background(), abi.FetchRequest{URL: srv.URL}); err != ErrEgressDenied {
		t.Fatalf("other-host fetch err = %v, want ErrEgressDenied", err)
	}
}

func TestFetchRejectsOversizedResponse(t *testing.T) {
	// A body exceeding FetchMaxBody must surface errResponseTooLarge rather than
	// being silently truncated to the cap.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, abi.FetchMaxBody+1))
	}))
	defer srv.Close()

	f := NewAllowlistFetcher([]string{"127.0.0.1"})
	f.AllowPrivateIPs = true
	if _, err := f.Fetch(context.Background(), abi.FetchRequest{URL: srv.URL}); err != errResponseTooLarge {
		t.Fatalf("oversized fetch err = %v, want errResponseTooLarge", err)
	}

	// A body exactly at the cap still succeeds and reads fully.
	exact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, abi.FetchMaxBody))
	}))
	defer exact.Close()
	resp, err := f.Fetch(context.Background(), abi.FetchRequest{URL: exact.URL})
	if err != nil {
		t.Fatalf("at-cap fetch: %v", err)
	}
	if len(resp.Body) != abi.FetchMaxBody {
		t.Fatalf("at-cap body = %d bytes, want %d", len(resp.Body), abi.FetchMaxBody)
	}
}

func TestFetchBlocksNonPublicIP(t *testing.T) {
	// A loopback target on the allowlist still gets blocked at dial time when the
	// non-public-IP guard is on (the default), preventing SSRF into the platform.
	f := NewAllowlistFetcher([]string{"127.0.0.1"})
	if _, err := f.Fetch(context.Background(), abi.FetchRequest{URL: "http://127.0.0.1:1/"}); err != ErrEgressDenied {
		t.Fatalf("loopback fetch err = %v, want ErrEgressDenied", err)
	}
}

func TestIsNonPublicIP(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":       true,  // loopback
		"::1":             true,  // loopback v6
		"10.0.0.5":        true,  // RFC1918
		"192.168.1.1":     true,  // RFC1918
		"172.16.0.1":      true,  // RFC1918
		"169.254.169.254": true,  // cloud metadata (link-local)
		"0.0.0.0":         true,  // unspecified
		"fc00::1":         true,  // ULA
		"fe80::1":         true,  // link-local v6
		"8.8.8.8":         false, // public
		"1.1.1.1":         false, // public
	}
	for s, want := range cases {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test IP %q", s)
		}
		if got := isNonPublicIP(ip); got != want {
			t.Errorf("isNonPublicIP(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestAllowlistSubdomainMatch(t *testing.T) {
	f := NewAllowlistFetcher([]string{"example.com"})
	cases := map[string]bool{
		"example.com":          true,
		"api.example.com":      true,
		"evil.com":             false,
		"notexample.com":       false,
		"example.com.evil.com": false,
	}
	for host, want := range cases {
		if got := f.allowed(host); got != want {
			t.Errorf("allowed(%q) = %v, want %v", host, got, want)
		}
	}
}
