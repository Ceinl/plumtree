package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// idleEvictAfter is how long an IP's bucket may sit untouched before it is
// swept, so the limiter's memory tracks active clients rather than all clients
// ever seen.
const idleEvictAfter = 10 * time.Minute

// ipLimiter is a per-client-IP token-bucket rate limiter. The clock is
// injectable for deterministic tests.
type ipLimiter struct {
	mu        sync.Mutex
	perSec    float64
	burst     float64
	now       func() time.Time
	buckets   map[string]*ipBucket
	lastSweep time.Time
}

type ipBucket struct {
	tokens float64
	last   time.Time
}

// newIPLimiter returns a limiter allowing perSec requests/sec per IP with the
// given burst (defaulting to perSec, min 1). perSec <= 0 returns nil, meaning
// "no limiting".
func newIPLimiter(perSec, burst int, now func() time.Time) *ipLimiter {
	if perSec <= 0 {
		return nil
	}
	b := float64(burst)
	if b < 1 {
		b = float64(perSec)
	}
	if b < 1 {
		b = 1
	}
	return &ipLimiter{
		perSec:    float64(perSec),
		burst:     b,
		now:       now,
		buckets:   make(map[string]*ipBucket),
		lastSweep: now(),
	}
}

// allow reports whether a request from ip may proceed, consuming a token.
func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	b := l.buckets[ip]
	if b == nil {
		b = &ipBucket{tokens: l.burst, last: now}
		l.buckets[ip] = b
	} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.last = now
		b.tokens += elapsed * l.perSec
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
	}

	l.sweepLocked(now)

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// sweepLocked drops buckets untouched for longer than idleEvictAfter. It runs
// at most once per idleEvictAfter to keep allow() cheap.
func (l *ipLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < idleEvictAfter {
		return
	}
	l.lastSweep = now
	for ip, b := range l.buckets {
		if now.Sub(b.last) >= idleEvictAfter {
			delete(l.buckets, ip)
		}
	}
}

// rateLimit wraps next, rejecting requests that exceed the per-IP budget with
// 429. /healthz is always allowed so health checks are never throttled. A nil
// limiter disables limiting.
func rateLimit(next http.Handler, l *ipLimiter) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the remote host. It deliberately ignores X-Forwarded-For:
// honoring it without a trusted-proxy allowlist would let clients spoof their
// IP to dodge the limit. Forwarded-header support is a separate, configured
// concern.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
