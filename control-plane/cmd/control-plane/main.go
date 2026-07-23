package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	buildworker "github.com/Ceinl/plumtree/build-worker"
	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/buildassets"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/control-plane/internal/gatewaybackend"
	"github.com/Ceinl/plumtree/control-plane/internal/httpapi"
	"github.com/Ceinl/plumtree/runner"
	"github.com/Ceinl/plumtree/ssh-gateway/gateway"
)

const (
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 5 * time.Minute
	httpIdleTimeout       = 1 * time.Minute
	httpShutdownTimeout   = 10 * time.Second
)

// buildBackend selects the source-to-WASM build implementation. A non-empty URL
// targets a separate build-worker process; otherwise an in-process sandboxed
// builder is used. The in-process builder defaults to the SDK, TUI runtime, and
// offline module proxy embedded in the server binary. devRoot replaces those
// assets with modules from a repository checkout for local development.
func buildBackend(url, token, devRoot string) (httpapi.BuildBackend, func() error, error) {
	if url != "" {
		return buildworker.NewClient(url, token), func() error { return nil }, nil
	}
	if _, err := exec.LookPath("go"); err != nil {
		return nil, nil, errors.New("in-process builds require a Go toolchain on PATH (or configure -build-url)")
	}
	cfg := buildworker.Config{}
	if devRoot != "" {
		mods, err := workspaceModules(devRoot)
		if err != nil {
			return nil, nil, err
		}
		cfg.WorkspaceModules = mods
		// The workspace provides the unpublished SDK/runtime; their transitive
		// dependencies still resolve through the operator's proxy.
		cfg.GoProxy = env("GOPROXY", "https://proxy.golang.org,direct")
		return buildworker.NewBuilder(cfg), func() error { return nil }, nil
	}

	assets, err := buildassets.Extract()
	if err != nil {
		return nil, nil, fmt.Errorf("prepare embedded build dependencies: %w", err)
	}
	cfg.WorkspaceModules = assets.WorkspaceModules
	cfg.GoProxy = assets.GoProxy
	return buildworker.NewBuilder(cfg), assets.Cleanup, nil
}

// workspaceModules validates the local development modules under devRoot.
func workspaceModules(devRoot string) ([]string, error) {
	var mods []string
	for _, name := range []string{"sdk", "tui-runtime"} {
		dir := filepath.Join(devRoot, name)
		fi, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err != nil || fi.IsDir() {
			return nil, fmt.Errorf("build dev root %q does not contain a complete %s module", devRoot, name)
		}
		mods = append(mods, dir)
	}
	return mods, nil
}

func main() {
	// Resolve and load the optional config file first so its values can seed the
	// flag defaults. Precedence: flag > env var > config file > built-in default.
	configPath := configPathFromArgs(os.Args[1:], os.Getenv("PLUMTREE_CONFIG"))
	fileCfg, err := loadConfig(configPath)
	if err != nil {
		log.Fatal(err)
	}
	fileTTL, _ := fileCfg.deployClaimTTL() // already validated by loadConfig

	flag.String("config", configPath, "path to a JSON operator config file (PLUMTREE_CONFIG)")
	addr := flag.String("addr", env("PLUMTREE_ADDR", "127.0.0.1:8080"), "HTTP listen address")
	origin := flag.String("origin", env("PLUMTREE_PUBLIC_ORIGIN", firstNonEmpty(fileCfg.PublicOrigin, "http://localhost:8080")), "public dashboard origin")
	shooBase := flag.String("shoo-base-url", env("SHOO_BASE_URL", shoo.DefaultBaseURL), "Shoo base URL")
	autoClaimOwner := flag.String("auto-claim-owner", env("PLUMTREE_AUTO_CLAIM_OWNER", fileCfg.AutoClaimOwner), "claim new deploys directly to this owner handle without Shoo (trusted self-hosting only)")
	devTokenDefault, devTokenEnvSet := os.LookupEnv("PLUMTREE_DEV_TOKEN")
	devToken := flag.String("dev-token", devTokenDefault, "enable local dev deploy API with this token; defaults to a generated local token outside production")
	gatewayToken := flag.String("gateway-token", env("PLUMTREE_GATEWAY_TOKEN", ""), "enable the gateway API (/internal/gateway) for a standalone SSH gateway with this shared token")
	stateFile := flag.String("state-file", env("PLUMTREE_STATE_FILE", defaultStateFile()), "persistent state file; empty disables persistence")
	stateEncryptionKeyFile := flag.String("state-encryption-key-file", env("PLUMTREE_STATE_ENCRYPTION_KEY_FILE", ""), "file containing a base64 32-byte snapshot KEK; mount it outside the state volume")
	previousStateEncryptionKeyFile := flag.String("previous-state-encryption-key-file", env("PLUMTREE_PREVIOUS_STATE_ENCRYPTION_KEY_FILE", ""), "previous snapshot KEK for one-time re-encryption during key rotation")
	blobDir := flag.String("blob-dir", env("PLUMTREE_BLOB_DIR", ""), "directory for durable WASM artifact storage; empty keeps artifacts in the state file")
	runnerWorker := flag.String("runner-worker", env("PLUMTREE_RUNNER_WORKER", ""), "path to the plumtree-runner-worker binary; set to isolate each app session in a separate process")
	runnerEndpoint := flag.String("runner-endpoint", env("PLUMTREE_RUNNER_ENDPOINT", ""), "remote runner broker endpoint (required for embedded SSH in production)")
	runnerToken := flag.String("runner-token", env("PLUMTREE_RUNNER_TOKEN", ""), "shared token for the remote runner broker")
	sshAddr := flag.String("ssh-addr", env("PLUMTREE_SSH_ADDR", "127.0.0.1:2222"), "SSH gateway listen address; empty disables SSH")
	sshHost := flag.String("ssh-host", env("PLUMTREE_SSH_HOST", fileCfg.SSHHost), "optional local SSH host alias to write to ~/.ssh/config; empty prints a raw SSH command")
	noSSHConfig := flag.Bool("no-ssh-config", false, "do not update ~/.ssh/config for the local SSH gateway")
	tailscaleMode := flag.Bool("tailscale", envBool("PLUMTREE_TAILSCALE", false), "detect this machine's Tailscale IPv4 address and advertise HTTP and SSH on it")
	maxFPS := flag.Int("max-fps", 60, "SSH repaint cap")
	maxSessions := flag.Int("max-sessions", envInt("PLUMTREE_MAX_SESSIONS", gateway.DefaultMaxConcurrentSessions), "max concurrent SSH sessions on this runner; 0 = unlimited")
	sessionTimeout := flag.Duration("session-timeout", envDuration("PLUMTREE_SESSION_TIMEOUT", runner.DefaultLimits.SessionTimeout), "maximum lifetime of one app session; 0 disables")
	sshHandshakeTimeout := flag.Duration("ssh-handshake-timeout", envDuration("PLUMTREE_SSH_HANDSHAKE_TIMEOUT", gateway.DefaultHandshakeTimeout), "maximum time allowed for an SSH handshake; negative disables")
	sshIdleTimeout := flag.Duration("ssh-idle-timeout", envDuration("PLUMTREE_SSH_IDLE_TIMEOUT", gateway.DefaultIdleTimeout), "disconnect an established SSH connection after this much network inactivity; negative disables")
	maxConnections := flag.Int("max-connections", envInt("PLUMTREE_MAX_CONNECTIONS", gateway.DefaultMaxConnections), "maximum admitted SSH TCP connections; negative disables")
	maxConnectionsPerIP := flag.Int("max-connections-per-ip", envInt("PLUMTREE_MAX_CONNECTIONS_PER_IP", gateway.DefaultMaxConnectionsPerIP), "maximum admitted SSH TCP connections per client IP; negative disables")
	maxSessionsPerAppDay := flag.Int("max-sessions-per-app-day", envInt("PLUMTREE_MAX_SESSIONS_PER_APP_DAY", firstPositiveInt(fileCfg.MaxSessionsPerAppPerDay, 50)), "max sessions per app per rolling 24h; 0 = unlimited")
	maxDeploysPerHour := flag.Int("max-deploys-per-hour", envInt("PLUMTREE_MAX_DEPLOYS_PER_HOUR", firstPositiveInt(fileCfg.MaxDeploysPerHour, 100)), "max new deploy claims across the platform per rolling hour; 0 = unlimited")
	maxAppsPerOwner := flag.Int("max-apps-per-owner", envInt("PLUMTREE_MAX_APPS_PER_OWNER", firstPositiveInt(fileCfg.MaxAppsPerOwner, 25)), "max apps a single owner may create; 0 = unlimited")
	deployClaimTTL := flag.Duration("deploy-claim-ttl", envDuration("PLUMTREE_DEPLOY_CLAIM_TTL", firstDuration(fileTTL, control.DeployClaimTTL)), "how long an unclaimed deploy may exist before garbage collection")
	anonPreview := flag.Bool("anonymous-preview", envBool("PLUMTREE_ANONYMOUS_PREVIEW", false), "allow running any deploy unclaimed at ssh preview-<deployID>@host, in the tightest sandbox")
	allowHostCommands := flag.Bool("allow-host-commands", envBool("PLUMTREE_ALLOW_HOST_COMMANDS", fileCfg.AllowHostCommands), "allow claimed apps to execute local programs as the server user (trusted self-hosting only)")
	rateLimit := flag.Int("rate-limit", envInt("PLUMTREE_RATE_LIMIT", 20), "dashboard/API requests per second per client IP; 0 = unlimited")
	rateBurst := flag.Int("rate-burst", envInt("PLUMTREE_RATE_BURST", 40), "dashboard/API rate-limit burst per client IP")
	seedDemo := flag.Bool("seed-demo", false, "seed a demo owner/app for local UI development")
	buildURL := flag.String("build-url", env("PLUMTREE_BUILD_URL", ""), "remote build-worker URL; empty uses an in-process sandboxed builder")
	buildToken := flag.String("build-token", env("PLUMTREE_BUILD_TOKEN", ""), "shared token sent to the remote build-worker")
	buildDevRoot := flag.String("build-dev-root", env("PLUMTREE_DEV_ROOT", ""), "override the embedded sdk/ and tui-runtime/ with modules from a local repository root (development only)")
	maxConcurrentBuilds := flag.Int("max-concurrent-builds", envInt("PLUMTREE_MAX_CONCURRENT_BUILDS", 2), "max simultaneous source builds; 0 = unlimited")
	maxQueuedBuilds := flag.Int("max-queued-builds", envInt("PLUMTREE_MAX_QUEUED_BUILDS", 8), "max source builds waiting for capacity; 0 rejects when busy")
	production := flag.Bool("production", envBool("PLUMTREE_PRODUCTION", false), "enable production safety checks")
	ackUnlimited := flag.Bool("acknowledge-unlimited-limits", envBool("PLUMTREE_ACKNOWLEDGE_UNLIMITED_LIMITS", false), "allow production startup with critical limits disabled")
	flag.Parse()
	setFlags := visitedFlagNames()
	if *autoClaimOwner != "" {
		if err := control.ValidateName(*autoClaimOwner); err != nil {
			log.Fatalf("invalid auto-claim owner: %v", err)
		}
	}
	if *tailscaleMode {
		ip, err := detectTailscaleIPv4(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		applyTailscaleDefaults(ip, addr, origin, sshAddr, networkOverrides{
			addr:   setFlags["addr"] || envIsSet("PLUMTREE_ADDR"),
			origin: setFlags["origin"] || envIsSet("PLUMTREE_PUBLIC_ORIGIN") || fileCfg.PublicOrigin != "",
			ssh:    setFlags["ssh-addr"] || envIsSet("PLUMTREE_SSH_ADDR"),
		})
	}
	managedDevToken := false
	managedDevTokenPath := ""
	if !*production && !setFlags["dev-token"] && !devTokenEnvSet {
		token, path, err := loadOrCreateDevToken()
		if err != nil {
			log.Fatal(err)
		}
		*devToken = token
		managedDevToken = true
		managedDevTokenPath = path
	}
	if err := validateProductionLimits(*production, *ackUnlimited, *sshAddr != "", productionLimits{
		maxSessions: *maxSessions, maxSessionsPerAppDay: *maxSessionsPerAppDay,
		maxDeploysPerHour: *maxDeploysPerHour, maxAppsPerOwner: *maxAppsPerOwner,
		maxConcurrentBuilds: *maxConcurrentBuilds, rateLimit: *rateLimit,
		maxConnections: *maxConnections, maxConnectionsPerIP: *maxConnectionsPerIP,
		sshHandshakeTimeout: *sshHandshakeTimeout, sshIdleTimeout: *sshIdleTimeout,
		runnerSessionTimeout: *sessionTimeout,
		runnerWorker:         *runnerWorker, runnerEndpoint: *runnerEndpoint, runnerToken: *runnerToken,
	}); err != nil {
		log.Fatal(err)
	}
	if *production && *stateFile != "" && *stateEncryptionKeyFile == "" {
		log.Fatal("production persistent state requires -state-encryption-key-file (mount a managed secret outside the data volume)")
	}

	verifier, err := shoo.NewVerifier(shoo.Config{
		BaseURL:   *shooBase,
		Issuer:    strings.TrimRight(*shooBase, "/"),
		AppOrigin: *origin,
	})
	if err != nil {
		log.Fatal(err)
	}
	storeOpts := []control.Option{
		control.WithMaxSessionsPerAppPerDay(*maxSessionsPerAppDay),
		control.WithMaxDeployClaimsPerHour(*maxDeploysPerHour),
		control.WithDefaultMaxApps(*maxAppsPerOwner),
		control.WithDeployClaimTTL(*deployClaimTTL),
		control.WithAnonymousPreview(*anonPreview),
	}
	if *stateEncryptionKeyFile != "" {
		key, err := readSnapshotEncryptionKey(*stateEncryptionKeyFile)
		if err != nil {
			log.Fatal(err)
		}
		storeOpts = append(storeOpts, control.WithSnapshotEncryptionKey(key))
	}
	if *previousStateEncryptionKeyFile != "" {
		key, err := readSnapshotEncryptionKey(*previousStateEncryptionKeyFile)
		if err != nil {
			log.Fatal(err)
		}
		storeOpts = append(storeOpts, control.WithPreviousSnapshotEncryptionKey(key))
	}
	if *blobDir != "" {
		storeOpts = append(storeOpts, control.WithBlobDir(*blobDir))
	}
	store, err := control.OpenStore(*stateFile, storeOpts...)
	if err != nil {
		log.Fatal(err)
	}
	if *seedDemo {
		seed(store)
	}

	build, cleanupBuild, err := buildBackend(*buildURL, *buildToken, *buildDevRoot)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := cleanupBuild(); err != nil {
			log.Printf("clean up embedded build dependencies: %v", err)
		}
	}()

	handler := httpapi.NewWithConfig(httpapi.Config{
		Store:               store,
		Verifier:            verifier,
		AppOrigin:           *origin,
		DevToken:            *devToken,
		AutoClaimOwner:      *autoClaimOwner,
		GatewayToken:        *gatewayToken,
		Build:               build,
		MaxConcurrentBuilds: *maxConcurrentBuilds,
		MaxQueuedBuilds:     *maxQueuedBuilds,
		RateLimitPerSec:     *rateLimit,
		RateLimitBurst:      *rateBurst,
	}).Handler()
	originURL := strings.TrimRight(*origin, "/")
	fmt.Println()
	fmt.Println("Plumtree control plane")
	fmt.Printf("  dashboard:  %s/dashboard\n", originURL)
	fmt.Printf("  http api:   %s\n", *addr)
	if *stateFile != "" {
		fmt.Printf("  state:      %s\n", *stateFile)
	} else {
		fmt.Println("  state:      in-memory (ephemeral)")
	}
	if *buildURL != "" {
		fmt.Printf("  build:      remote worker %s\n", *buildURL)
	} else {
		fmt.Println("  build:      in-process sandbox")
	}
	if configPath != "" {
		fmt.Printf("  config:     %s\n", configPath)
	}

	fmt.Println()
	fmt.Printf("Limits: %s apps/owner · %s connections/app/day · %s new deploys/hour · claim window %s\n",
		unlimitedOr(*maxAppsPerOwner), unlimitedOr(*maxSessionsPerAppDay), unlimitedOr(*maxDeploysPerHour), *deployClaimTTL)

	fmt.Println()
	fmt.Println("Authors — deploy, then claim to own the app:")
	if *devToken != "" {
		fmt.Println("  pt deploy            build & upload the current app (server-side)")
		if *autoClaimOwner != "" {
			fmt.Printf("  auto-claim:          new deploys are owned by %s (Shoo claim disabled)\n", *autoClaimOwner)
		} else {
			fmt.Printf("  pt claim             open the browser claim to take ownership (within %s)\n", *deployClaimTTL)
		}
	} else {
		fmt.Println("  deploy is disabled — start with -dev-token TOKEN to allow `pt deploy`")
	}
	if *gatewayToken != "" {
		fmt.Println("  gateway API enabled at /internal/gateway (for a standalone ssh-gateway)")
	}
	if *allowHostCommands {
		fmt.Fprintln(os.Stderr, "WARNING: host commands enabled; claimed apps execute with the server user's authority")
	}
	if *devToken != "" {
		fmt.Println()
		fmt.Println("Client setup:")
		if managedDevToken && !*tailscaleMode {
			fmt.Println("  pt deploy            uses the generated local token automatically")
		} else {
			fmt.Printf("  pt configure --addr %s --token\n", originURL)
		}
		if managedDevToken && *tailscaleMode {
			fmt.Printf("  token: %s\n", *devToken)
			fmt.Printf("  saved: %s\n", managedDevTokenPath)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 2)

	httpServer := newHTTPServer(*addr, handler)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	if *sshAddr != "" {
		limits := runner.DefaultLimits
		limits.SessionTimeout = *sessionTimeout
		gw := &gateway.Server{
			Backend:               gatewaybackend.New(store),
			Limits:                limits,
			MaxFPS:                *maxFPS,
			MaxConcurrentSessions: *maxSessions,
			HandshakeTimeout:      *sshHandshakeTimeout,
			IdleTimeout:           *sshIdleTimeout,
			MaxConnections:        *maxConnections,
			MaxConnectionsPerIP:   *maxConnectionsPerIP,
			StateDir:              stateDir(*stateFile),
			RunnerWorker:          *runnerWorker,
			RunnerEndpoint:        *runnerEndpoint,
			RunnerToken:           *runnerToken,
			AllowHostCommands:     *allowHostCommands,
			Logf:                  func(f string, a ...any) { fmt.Fprintf(os.Stderr, "  "+f+"\n", a...) },
			Ready: func(a net.Addr) {
				host, port, _ := net.SplitHostPort(a.String())
				connectHost := gateway.HostFromListen(host)

				// connect renders the ssh command a user runs for a given handle,
				// matching whichever connection style is active (alias vs raw port).
				var connect func(handle string) string
				fmt.Println()
				switch {
				case !shouldInstallDevSSHConfig(*sshHost, *noSSHConfig):
					fmt.Printf("Users connect over SSH (gateway %s:%s):\n", connectHost, port)
					connect = func(h string) string {
						return fmt.Sprintf("ssh -p %s -o HostKeyAlias=plumtree-dev -o StrictHostKeyChecking=accept-new %s@%s", port, h, connectHost)
					}
				default:
					if path, err := installDevSSHConfig(*sshHost, connectHost, port); err == nil {
						fmt.Printf("Users connect over SSH (gateway %s:%s, aliased %q in %s):\n", connectHost, port, *sshHost, path)
						connect = func(h string) string { return fmt.Sprintf("ssh %s@%s", h, *sshHost) }
					} else {
						fmt.Fprintf(os.Stderr, "ssh config update failed: %v\n", err)
						fmt.Printf("Users connect over SSH (gateway %s:%s):\n", connectHost, port)
						connect = func(h string) string { return fmt.Sprintf("ssh -p %s %s@%s", port, h, connectHost) }
					}
				}
				fmt.Printf("  claimed app:         %s\n", connect("<app>"))
				if *anonPreview {
					fmt.Printf("  unclaimed preview:   %s\n", connect("preview-<deployID>"))
				}
			},
		}
		go func() {
			if err := gw.ListenAndServe(ctx, *sshAddr); err != nil {
				errCh <- err
			}
		}()
	} else {
		fmt.Println()
		fmt.Println("SSH gateway disabled (-ssh-addr empty); deployed apps are not connectable here.")
	}

	select {
	case err := <-errCh:
		log.Fatal(err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown: %v", err)
		}
	}
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

// firstDuration returns the first positive duration, so a config value wins over
// the built-in default only when it is actually set.
func firstDuration(values ...time.Duration) time.Duration {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

// firstPositiveInt returns the first value greater than zero, so a config value
// overrides the built-in default only when it is actually set.
func firstPositiveInt(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

// unlimitedOr renders a 0 limit as "unlimited" for startup logging.
func unlimitedOr(n int) string {
	if n <= 0 {
		return "unlimited"
	}
	return strconv.Itoa(n)
}

type productionLimits struct {
	maxSessions, maxSessionsPerAppDay, maxDeploysPerHour, maxAppsPerOwner int
	maxConcurrentBuilds, rateLimit, maxConnections, maxConnectionsPerIP   int
	sshHandshakeTimeout, sshIdleTimeout                                   time.Duration
	runnerSessionTimeout                                                  time.Duration
	runnerWorker, runnerEndpoint, runnerToken                             string
}

func validateProductionLimits(production, acknowledged, sshEnabled bool, l productionLimits) error {
	if l.runnerEndpoint != "" && l.runnerWorker != "" {
		return fmt.Errorf("configure either runner-endpoint or runner-worker, not both")
	}
	if l.runnerEndpoint != "" && l.runnerToken == "" {
		return fmt.Errorf("runner-token is required with runner-endpoint")
	}
	if production && sshEnabled && l.runnerEndpoint == "" {
		return fmt.Errorf("production embedded SSH requires a remote runner-endpoint; a local subprocess does not contain native runtime escape")
	}
	if production && sshEnabled && !strings.HasPrefix(l.runnerEndpoint, "unix://") {
		return fmt.Errorf("production embedded SSH runner-endpoint must use an authenticated Unix socket")
	}
	if !production || acknowledged {
		return nil
	}
	var unlimited []string
	for name, value := range map[string]int{
		"max-sessions-per-app-day": l.maxSessionsPerAppDay, "max-deploys-per-hour": l.maxDeploysPerHour,
		"max-apps-per-owner": l.maxAppsPerOwner, "max-concurrent-builds": l.maxConcurrentBuilds,
		"rate-limit": l.rateLimit,
	} {
		if value <= 0 {
			unlimited = append(unlimited, name)
		}
	}
	if sshEnabled {
		if l.maxSessions <= 0 {
			unlimited = append(unlimited, "max-sessions")
		}
		if l.maxConnections < 0 {
			unlimited = append(unlimited, "max-connections")
		}
		if l.maxConnectionsPerIP < 0 {
			unlimited = append(unlimited, "max-connections-per-ip")
		}
		if l.sshHandshakeTimeout < 0 {
			unlimited = append(unlimited, "ssh-handshake-timeout")
		}
		if l.sshIdleTimeout < 0 {
			unlimited = append(unlimited, "ssh-idle-timeout")
		}
		if l.runnerSessionTimeout <= 0 {
			unlimited = append(unlimited, "session-timeout")
		}
	}
	if len(unlimited) == 0 {
		return nil
	}
	slices.Sort(unlimited)
	return fmt.Errorf("refusing production startup with unlimited critical limits: %s (set PLUMTREE_ACKNOWLEDGE_UNLIMITED_LIMITS=true to acknowledge)", strings.Join(unlimited, ", "))
}

// readSnapshotEncryptionKey reads the base64-encoded 32-byte KEK supplied by a
// secret manager mount. Keeping the file path separate from /data prevents a
// copied volume from being sufficient to decrypt the snapshot.
func readSnapshotEncryptionKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read state encryption key %q: %w", path, err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("decode state encryption key %q: %w", path, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("state encryption key %q must decode to exactly 32 bytes", path)
	}
	return key, nil
}

func defaultStateFile() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "plumtree", "control-plane-state.json")
	}
	return ""
}

// stateDir is where per-app data (KV stores) is persisted. It sits beside the
// state file, or falls back to the OS config dir when state is in-memory only.
func stateDir(stateFile string) string {
	if stateFile != "" {
		return filepath.Dir(stateFile)
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "plumtree")
	}
	return ""
}

func seed(store *control.Store) {
	owner, err := store.EnsureOwner("demo")
	if err != nil {
		log.Fatal(err)
	}
	app, err := store.EnsureApp(control.AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		log.Fatal(err)
	}
	artifact, err := store.CreateArtifact(control.ArtifactInput{
		Digest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes:  2_621_440,
		ABIVersion: 0,
	})
	if err != nil {
		log.Fatal(err)
	}
	deploy, err := store.CreateDeploy(control.DeployInput{
		AppID:            app.ID,
		ArtifactID:       artifact.ID,
		SourceDigest:     "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CreatedByOwnerID: owner.ID,
	})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := store.ActivateDeploy(app.ID, deploy.ID); err != nil {
		log.Fatal(err)
	}
}

const (
	devSSHHostKeyAlias = "plumtree-dev"
	sshConfigBegin     = "# BEGIN PLUMTREE DEV"
	sshConfigEnd       = "# END PLUMTREE DEV"
)

func shouldInstallDevSSHConfig(host string, disabled bool) bool {
	return !disabled && strings.TrimSpace(host) != ""
}

func installDevSSHConfig(host, targetHost, port string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ssh")
	path := filepath.Join(dir, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	var existing []byte
	if b, err := os.ReadFile(path); err == nil {
		existing = b
	} else if !os.IsNotExist(err) {
		return "", err
	}
	next := replaceManagedSSHBlock(string(existing), devSSHConfigBlock(host, targetHost, port))
	if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func devSSHConfigBlock(host, targetHost, port string) string {
	return fmt.Sprintf(`%s
Host %s
    HostName %s
    Port %s
    HostKeyAlias %s
    StrictHostKeyChecking accept-new
%s
`, sshConfigBegin, host, targetHost, port, devSSHHostKeyAlias, sshConfigEnd)
}

func replaceManagedSSHBlock(existing, block string) string {
	existing = strings.TrimRight(existing, "\n")
	start := strings.Index(existing, sshConfigBegin)
	end := strings.Index(existing, sshConfigEnd)
	if start >= 0 && end >= start {
		end += len(sshConfigEnd)
		next := strings.TrimRight(existing[:start], "\n")
		tail := strings.TrimLeft(existing[end:], "\n")
		var parts []string
		if next != "" {
			parts = append(parts, next)
		}
		parts = append(parts, strings.TrimRight(block, "\n"))
		if tail != "" {
			parts = append(parts, tail)
		}
		return strings.Join(parts, "\n\n") + "\n"
	}
	if existing == "" {
		return block
	}
	return strings.TrimRight(block, "\n") + "\n\n" + existing + "\n"
}
