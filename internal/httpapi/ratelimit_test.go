package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIPLimiterPerIPAndRefill(t *testing.T) {
	now := time.Unix(0, 0)
	l := newIPLimiter(5, 5, func() time.Time { return now })

	for i := 0; i < 5; i++ {
		if !l.allow("1.1.1.1") {
			t.Fatalf("request %d within burst should pass", i)
		}
	}
	if l.allow("1.1.1.1") {
		t.Fatal("6th request should be limited")
	}
	if !l.allow("2.2.2.2") {
		t.Fatal("a different IP has its own bucket")
	}

	now = now.Add(time.Second) // refills 5 tokens
	if !l.allow("1.1.1.1") {
		t.Fatal("should pass again after refill")
	}
}

func TestIPLimiterDisabled(t *testing.T) {
	if newIPLimiter(0, 0, time.Now) != nil {
		t.Fatal("perSec <= 0 should yield a nil (disabled) limiter")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	now := time.Unix(0, 0)
	l := newIPLimiter(1, 1, func() time.Time { return now })
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := rateLimit(next, l)

	do := func(path, ip string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = ip + ":40000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	if got := do("/dashboard", "1.1.1.1"); got != http.StatusOK {
		t.Fatalf("first request = %d, want 200", got)
	}
	if got := do("/dashboard", "1.1.1.1"); got != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", got)
	}
	// /healthz is exempt regardless of budget.
	for i := 0; i < 5; i++ {
		if got := do("/healthz", "1.1.1.1"); got != http.StatusOK {
			t.Fatalf("healthz request %d = %d, want 200", i, got)
		}
	}
	// A different client IP is unaffected.
	if got := do("/dashboard", "9.9.9.9"); got != http.StatusOK {
		t.Fatalf("other IP = %d, want 200", got)
	}
}

func TestRateLimitNilPassesThrough(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := rateLimit(next, nil)
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("nil limiter should pass through, got %d", w.Code)
	}
}
