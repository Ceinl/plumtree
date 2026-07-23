package httpapi

import (
	"context"
	"net/http"
	"time"

	buildworker "github.com/Ceinl/plumtree/build-worker"
	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/ssh-gateway/gatewayapi"
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
	store        *control.Store
	verifier     TokenVerifier
	appOrigin    string
	devToken     string
	autoClaim    bool
	gatewayToken string
	build        BuildBackend
	buildSlots   chan struct{}
	buildQueue   chan struct{}
	limiter      *ipLimiter
	suspensions  *suspensionHub
}

func New(store *control.Store, verifier TokenVerifier, appOrigin string) *Server {
	return NewWithConfig(Config{Store: store, Verifier: verifier, AppOrigin: appOrigin})
}

type Config struct {
	Store     *control.Store
	Verifier  TokenVerifier
	AppOrigin string
	DevToken  string
	// AutoClaim claims every new deploy using the dev deploy token instead of
	// requiring the Shoo browser flow. This is intended only for trusted servers.
	AutoClaim bool
	// GatewayToken, when set, enables the operator-internal gateway API
	// (/internal/gateway/*) that a standalone SSH gateway calls to resolve apps
	// and record sessions. Empty disables those endpoints (all-in-one mode, where
	// the gateway runs in-process and talks to the store directly).
	GatewayToken string
	// Build, when set, compiles uploaded source server-side. When nil, deploys
	// must carry pre-built WASM (legacy/dev path).
	Build BuildBackend
	// MaxConcurrentBuilds bounds simultaneous calls to Build. Zero is unlimited.
	MaxConcurrentBuilds int
	// MaxQueuedBuilds bounds requests waiting for a build slot. Zero rejects
	// immediately when all build slots are occupied.
	MaxQueuedBuilds int
	// RateLimitPerSec caps requests per second per client IP across the
	// dashboard and API. 0 disables HTTP rate limiting. RateLimitBurst sets the
	// bucket depth (defaults to RateLimitPerSec).
	RateLimitPerSec int
	RateLimitBurst  int
}

func NewWithConfig(cfg Config) *Server {
	store := cfg.Store
	if store == nil {
		store = control.NewStore()
	}
	var buildSlots chan struct{}
	var buildQueue chan struct{}
	if cfg.MaxConcurrentBuilds > 0 {
		buildSlots = make(chan struct{}, cfg.MaxConcurrentBuilds)
		if cfg.MaxQueuedBuilds > 0 {
			buildQueue = make(chan struct{}, cfg.MaxQueuedBuilds)
		}
	}
	suspensions := newSuspensionHub()
	server := &Server{
		store:        store,
		verifier:     cfg.Verifier,
		appOrigin:    cfg.AppOrigin,
		devToken:     cfg.DevToken,
		autoClaim:    cfg.AutoClaim,
		gatewayToken: cfg.GatewayToken,
		build:        cfg.Build,
		buildSlots:   buildSlots,
		buildQueue:   buildQueue,
		limiter:      newIPLimiter(cfg.RateLimitPerSec, cfg.RateLimitBurst, time.Now),
		suspensions:  suspensions,
	}
	store.RegisterSuspensionListener(suspensions.publish)
	return server
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
	mux.HandleFunc("/api/apps/stream", s.handleAppsStream)
	mux.HandleFunc("/api/claims/", s.handleClaimAPI)
	mux.HandleFunc("/api/dev/deploy/", s.handleDevDeployPath)
	mux.HandleFunc("/api/dev/deploy", s.handleDevDeploy)
	mux.HandleFunc("/api/dev/ping", s.handleDevPing)
	mux.HandleFunc(gatewayapi.BasePath+"/identity", s.handleGatewayIdentity)
	mux.HandleFunc(gatewayapi.BasePath+"/resolve", s.handleGatewayResolve)
	mux.HandleFunc(gatewayapi.BasePath+"/sessions", s.handleGatewayStartSession)
	mux.HandleFunc(gatewayapi.BasePath+"/sessions/", s.handleGatewaySessionByID)
	mux.HandleFunc(gatewayapi.BasePath+"/apps/", s.handleGatewayApp)
	mux.HandleFunc(gatewayapi.BasePath+"/suspensions", s.handleGatewaySuspensions)
	mux.HandleFunc(gatewayapi.BasePath+"/suspensions/next", s.handleGatewaySuspensionNext)
	mux.HandleFunc(gatewayapi.BasePath+"/suspensions/ack", s.handleGatewaySuspensionAck)
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
		CSPNonce  string
	}{AppOrigin: s.appOrigin, CSPNonce: cspNonce(r.Context())})
}
