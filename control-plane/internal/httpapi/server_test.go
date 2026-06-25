package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(body, "https://shoo.dev/shoo.js") || !strings.Contains(body, "/api/apps") || !strings.Contains(body, "/api/me/handle") {
		t.Fatalf("dashboard missing Shoo client or API calls")
	}
}

func TestDashboardShowsPlatformLimits(t *testing.T) {
	server := NewWithConfig(Config{
		Store:           control.NewStore(),
		Verifier:        fakeVerifier{},
		RateLimitPerSec: 20,
		RateLimitBurst:  40,
		Limits: LimitsInfo{
			MaxConcurrentSessions:   8,
			MaxSessionsPerAppPerDay: 50,
			MaxFramesPerSec:         120,
			MaxEventsPerSec:         200,
			SessionTimeout:          30 * time.Minute,
		},
	})
	rec := serveTestRequest(t, server, http.MethodGet, "/dashboard", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Platform limits", "Connections / app / day", "20 req/s (burst 40)", "Concurrent sessions", "120 fps", "200 /s", "30m0s"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
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
