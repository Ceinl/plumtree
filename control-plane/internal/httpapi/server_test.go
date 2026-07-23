package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

type fakeVerifier struct {
	claims shoo.Claims
	err    error
}

func (f fakeVerifier) Verify(context.Context, string) (shoo.Claims, error) {
	return f.claims, f.err
}

func TestDashboardServesShooClient(t *testing.T) {
	server := New(control.NewStore(), fakeVerifier{}, "http://localhost:8080")
	rec := serveTestRequest(t, server, http.MethodGet, "/dashboard", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "https://shoo.dev/shoo.js") || !strings.Contains(body, "/api/apps") ||
		!strings.Contains(body, "/api/me/handle") || !strings.Contains(body, "/api/me/ssh-keys") {
		t.Fatalf("dashboard missing Shoo client or API calls")
	}
	if strings.Contains(body, "EventSource") || strings.Contains(body, "access_token") {
		t.Fatalf("dashboard exposes bearer credentials through an SSE URL")
	}
	if !strings.Contains(body, "Authorization: \"Bearer \" + token") {
		t.Fatalf("dashboard SSE fetch does not use the authorization header")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "script-src 'self' https://shoo.dev 'unsafe-inline'") {
		t.Fatalf("script CSP still permits unsafe-inline: %s", csp)
	}
	marker := "'nonce-"
	start := strings.Index(csp, marker)
	if start < 0 {
		t.Fatalf("CSP missing script nonce: %s", csp)
	}
	start += len(marker)
	end := strings.IndexByte(csp[start:], '\'')
	if end < 0 {
		t.Fatalf("malformed CSP nonce: %s", csp)
	}
	nonce := csp[start : start+end]
	if !strings.Contains(body, `nonce="`+nonce+`"`) {
		t.Fatalf("dashboard script does not carry CSP nonce %q", nonce)
	}
}

func TestClaimPageRendersUnquotedDeployID(t *testing.T) {
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret"})
	rec := serveTestRequest(t, server, http.MethodGet, "/claim/dep_000001/claim-token", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `const deployID = "dep_000001";`) {
		t.Fatalf("claim page deploy ID was not rendered as a plain JS string:\n%s", body)
	}
	if !strings.Contains(body, `const claimToken = "claim-token";`) {
		t.Fatalf("claim page token was not rendered as a plain JS string:\n%s", body)
	}
}

func TestCITokenRoutesAreNotExposedUntilCIOperationsAuthorizeThem(t *testing.T) {
	server := New(control.NewStore(), fakeVerifier{}, "http://localhost:8080")
	for _, path := range []string{"/api/me/tokens", "/api/me/tokens/token_000001"} {
		rec := serveTestRequest(t, server, http.MethodGet, path, nil, "test-token")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, rec.Code)
		}
	}
}
