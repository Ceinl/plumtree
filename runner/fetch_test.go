package runner

import (
	"context"
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

	// Allowlisted host: request goes through.
	f := NewAllowlistFetcher([]string{"127.0.0.1"})
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
