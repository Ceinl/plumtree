package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	buildworker "github.com/Ceinl/plumtree/build-worker"
	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

type TokenVerifier interface {
	Verify(ctx context.Context, token string) (shoo.Claims, error)
}

// BuildBackend compiles uploaded app source to a WASM artifact in a sandbox.
// It is satisfied by both the in-process *buildworker.Builder and the remote
// *buildworker.Client, so the control plane never hosts the toolchain itself.
type BuildBackend interface {
	Build(ctx context.Context, req buildworker.Request) (buildworker.Result, error)
}

type Server struct {
	store     *control.Store
	verifier  TokenVerifier
	appOrigin string
	devToken  string
	build     BuildBackend
	limiter   *ipLimiter
	limits    []limitRow
}

// limitRow is one labeled platform limit rendered on the dashboard.
type limitRow struct {
	Label string
	Value string
}

func New(store *control.Store, verifier TokenVerifier, appOrigin string) *Server {
	return NewWithConfig(Config{Store: store, Verifier: verifier, AppOrigin: appOrigin})
}

type Config struct {
	Store     *control.Store
	Verifier  TokenVerifier
	AppOrigin string
	DevToken  string
	// Build, when set, compiles uploaded source server-side. When nil, deploys
	// must carry pre-built WASM (legacy/dev path).
	Build BuildBackend
	// RateLimitPerSec caps requests per second per client IP across the
	// dashboard and API. 0 disables HTTP rate limiting. RateLimitBurst sets the
	// bucket depth (defaults to RateLimitPerSec).
	RateLimitPerSec int
	RateLimitBurst  int
	// Limits are the runner/session caps surfaced read-only on the dashboard.
	Limits LimitsInfo
}

// LimitsInfo is the set of platform limits shown on the dashboard. Zero values
// render as "unlimited".
type LimitsInfo struct {
	MaxConcurrentSessions   int
	MaxSessionsPerAppPerDay int
	MaxFramesPerSec         int
	MaxEventsPerSec         int
	SessionTimeout          time.Duration
}

func NewWithConfig(cfg Config) *Server {
	store := cfg.Store
	if store == nil {
		store = control.NewStore()
	}
	return &Server{
		store:     store,
		verifier:  cfg.Verifier,
		appOrigin: cfg.AppOrigin,
		devToken:  cfg.DevToken,
		build:     cfg.Build,
		limiter:   newIPLimiter(cfg.RateLimitPerSec, cfg.RateLimitBurst, time.Now),
		limits:    buildLimitRows(cfg),
	}
}

// buildLimitRows renders the configured platform limits for display, formatting
// zero values as "unlimited".
func buildLimitRows(cfg Config) []limitRow {
	perPerSec := func(n int, unit string) string {
		if n <= 0 {
			return "unlimited"
		}
		if unit == "" {
			return strconv.Itoa(n)
		}
		return fmt.Sprintf("%d %s", n, unit)
	}
	httpRate := "unlimited"
	if cfg.RateLimitPerSec > 0 {
		burst := cfg.RateLimitBurst
		if burst < 1 {
			burst = cfg.RateLimitPerSec
		}
		httpRate = fmt.Sprintf("%d req/s (burst %d)", cfg.RateLimitPerSec, burst)
	}
	sessionTimeout := "unlimited"
	if cfg.Limits.SessionTimeout > 0 {
		sessionTimeout = cfg.Limits.SessionTimeout.String()
	}
	return []limitRow{
		{"Connections / app / day", perPerSec(cfg.Limits.MaxSessionsPerAppPerDay, "")},
		{"Concurrent sessions", perPerSec(cfg.Limits.MaxConcurrentSessions, "")},
		{"HTTP rate / IP", httpRate},
		{"Input events", perPerSec(cfg.Limits.MaxEventsPerSec, "/s")},
		{"Frames", perPerSec(cfg.Limits.MaxFramesPerSec, "fps")},
		{"Session time budget", sessionTimeout},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/dashboard", s.handleDashboard)
	mux.HandleFunc("/shoo/callback", s.handleDashboard)
	mux.HandleFunc("/claim/", s.handleClaimPage)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/api/me/handle", s.handleMeHandle)
	mux.HandleFunc("/api/apps", s.handleApps)
	mux.HandleFunc("/api/me/tokens", s.handleTokens)
	mux.HandleFunc("/api/me/tokens/", s.handleTokenByID)
	mux.HandleFunc("/api/claims/", s.handleClaimAPI)
	mux.HandleFunc("/api/dev/deploy/", s.handleDevDeployPath)
	mux.HandleFunc("/api/dev/deploy", s.handleDevDeploy)
	return securityHeaders(rateLimit(mux, s.limiter))
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = dashboardTmpl.Execute(w, struct {
		AppOrigin string
		Limits    []limitRow
	}{AppOrigin: s.appOrigin, Limits: s.limits})
}
