package runner

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
		"100.64.0.1":      true,  // shared/overlay address space
		"198.18.0.1":      true,  // benchmarking range
		"203.0.113.1":     true,  // documentation/special-purpose
		"240.0.0.1":       true,  // reserved
		"0.0.0.0":         true,  // unspecified
		"fc00::1":         true,  // ULA
		"fe80::1":         true,  // link-local v6
		"2001:db8::1":     true,  // documentation v6
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

// Matching semantics: a bare entry matches exactly that host; "." and "*"
// wildcards match the domain plus its subdomains; malformed entries never
// match anything, including lookalike hosts.
func TestAllowlistMatchingSemantics(t *testing.T) {
	tests := []struct {
		name  string
		allow []string
		host  string
		want  bool
	}{
		{"bare matches exact", []string{"example.com"}, "example.com", true},
		{"bare does not match subdomain", []string{"example.com"}, "api.example.com", false},
		{"bare is not suffix-matched", []string{"example.com"}, "example.com.evil.com", false},
		{"bare does not match lookalike", []string{"example.com"}, "notexample.com", false},
		{"bare does not match other", []string{"example.com"}, "evil.com", false},
		{"dot wildcard matches domain", []string{".example.com"}, "example.com", true},
		{"dot wildcard matches subdomain", []string{".example.com"}, "api.example.com", true},
		{"dot wildcard matches deep subdomain", []string{"*.example.com"}, "a.b.example.com", true},
		{"star wildcard matches domain", []string{"*.example.com"}, "example.com", true},
		{"star wildcard matches subdomain", []string{"*.example.com"}, "api.example.com", true},
		{"star wildcard is not prefix-greedy", []string{"*.star.com"}, "notstar.com", false},
		{"wildcard is not suffix-hijacked", []string{".example.com"}, "example.com.evil.com", false},
		{"empty entry never matches", []string{""}, "example.com", false},
		{"bare dot never matches", []string{"."}, "anything.com", false},
		{"double dot never matches", []string{".."}, "anything.com", false},
		{"star alone never matches", []string{"*"}, "example.com", false},
		{"star dot never matches", []string{"*."}, "example.com", false},
		{"entry with space never matches", []string{"ex ample.com"}, "ex ample.com", false},
		{"case-insensitive entry", []string{"EXAMPLE.com"}, "example.com", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := NewAllowlistFetcher(tc.allow)
			if got := f.allowed(tc.host); got != tc.want {
				t.Errorf("allowed(%q) with allow %q = %v, want %v", tc.host, tc.allow, got, tc.want)
			}
		})
	}
}

func TestNewValidatedAllowlistFetcher(t *testing.T) {
	valid := [][]string{
		nil,
		{"example.com"},
		{".example.com"},
		{"*.example.com"},
		{"a-b.example.com", "xn--80ak6aa92e.com"},
	}
	for _, allow := range valid {
		if _, err := NewValidatedAllowlistFetcher(allow); err != nil {
			t.Errorf("NewValidatedAllowlistFetcher(%q) err = %v, want nil", allow, err)
		}
	}

	rejected := [][]string{
		{""},
		{"."},
		{".."},
		{"*"},
		{"*."},
		{"*..example.com"},
		{"bad host.com"},                    // space
		{"-leadinghyphen.com"},              // leading hyphen
		{"trailinghyphen-.com"},             // trailing hyphen label
		{"empty..label.com"},                // empty label
		{strings.Repeat("a", 64) + ".com"},  // label over 63 bytes
		{strings.Repeat("a.", 127) + "com"}, // total length over 253
	}
	for _, allow := range rejected {
		f, err := NewValidatedAllowlistFetcher(allow)
		if err == nil {
			t.Errorf("NewValidatedAllowlistFetcher(%q) accepted, want error", allow)
			continue
		}
		if f != nil {
			t.Errorf("NewValidatedAllowlistFetcher(%q) returned a fetcher alongside an error", allow)
		}
		for _, entry := range allow {
			if !strings.Contains(err.Error(), entry) && strings.TrimSpace(entry) != "" {
				t.Errorf("error for %q does not list offending entry: %v", allow, err)
			}
		}
	}

	// A rejected list must name the offender so operators can fix config fast.
	_, err := NewValidatedAllowlistFetcher([]string{"ok.example.com", "bad host.com"})
	if err == nil || !strings.Contains(err.Error(), `"bad host.com"`) {
		t.Fatalf("mixed-list error = %v, want the offending entry named", err)
	}
}
