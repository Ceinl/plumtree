package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	buildworker "github.com/Ceinl/plumtree/build-worker"
	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/control-plane/internal/gatewaybackend"
	"github.com/Ceinl/plumtree/control-plane/internal/httpapi"
	"github.com/Ceinl/plumtree/runner"
	"github.com/Ceinl/plumtree/ssh-gateway/gateway"
)

// buildBackend selects the source-to-WASM build implementation. A non-empty URL
// targets a separate build-worker process; otherwise an in-process sandboxed
// builder is used. devRoot, when it contains sibling sdk/ and tui-runtime/
// modules, ties them into the build workspace so the in-process builder resolves
// the unpublished SDK without a registry — a local development convenience. In
// production the SDK is published and resolved through GOPROXY, so devRoot is
// left unset and the build stays fully hermetic (GOPROXY=off).
func buildBackend(url, token, devRoot string) httpapi.BuildBackend {
	if url != "" {
		return buildworker.NewClient(url, token)
	}
	cfg := buildworker.Config{}
	if mods := workspaceModules(devRoot); len(mods) > 0 {
		cfg.WorkspaceModules = mods
		// The workspace provides the unpublished SDK/runtime; their transitive
		// dependencies still resolve through the operator's proxy.
		cfg.GoProxy = env("GOPROXY", "https://proxy.golang.org,direct")
	}
	return buildworker.NewBuilder(cfg)
}

// workspaceModules returns the local module directories under devRoot to add to
// the build workspace, or nil when devRoot is unset or incomplete.
func workspaceModules(devRoot string) []string {
	if devRoot == "" {
		return nil
	}
	var mods []string
	for _, name := range []string{"sdk", "tui-runtime"} {
		dir := filepath.Join(devRoot, name)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			mods = append(mods, dir)
		}
	}
	return mods
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
	addr := flag.String("addr", env("PLUMTREE_ADDR", ":8080"), "HTTP listen address")
	origin := flag.String("origin", env("PLUMTREE_PUBLIC_ORIGIN", firstNonEmpty(fileCfg.PublicOrigin, "http://localhost:8080")), "public dashboard origin")
	shooBase := flag.String("shoo-base-url", env("SHOO_BASE_URL", shoo.DefaultBaseURL), "Shoo base URL")
	devToken := flag.String("dev-token", env("PLUMTREE_DEV_TOKEN", ""), "enable local dev deploy API with this token")
	gatewayToken := flag.String("gateway-token", env("PLUMTREE_GATEWAY_TOKEN", ""), "enable the gateway API (/internal/gateway) for a standalone SSH gateway with this shared token")
	stateFile := flag.String("state-file", env("PLUMTREE_STATE_FILE", defaultStateFile()), "persistent state file; empty disables persistence")
	blobDir := flag.String("blob-dir", env("PLUMTREE_BLOB_DIR", ""), "directory for durable WASM artifact storage; empty keeps artifacts in the state file")
	runnerWorker := flag.String("runner-worker", env("PLUMTREE_RUNNER_WORKER", ""), "path to the plumtree-runner-worker binary; set to isolate each TUI session in a separate process")
	sshAddr := flag.String("ssh-addr", env("PLUMTREE_SSH_ADDR", "127.0.0.1:2222"), "SSH gateway listen address; empty disables SSH")
	sshHost := flag.String("ssh-host", env("PLUMTREE_SSH_HOST", firstNonEmpty(fileCfg.SSHHost, "plumtree.dev")), "local SSH host alias written to ~/.ssh/config")
	noSSHConfig := flag.Bool("no-ssh-config", false, "do not update ~/.ssh/config for the local SSH gateway")
	maxFPS := flag.Int("max-fps", 60, "SSH repaint cap")
	maxSessions := flag.Int("max-sessions", envInt("PLUMTREE_MAX_SESSIONS", 0), "max concurrent SSH sessions on this runner; 0 = unlimited")
	maxSessionsPerAppDay := flag.Int("max-sessions-per-app-day", envInt("PLUMTREE_MAX_SESSIONS_PER_APP_DAY", firstPositiveInt(fileCfg.MaxSessionsPerAppPerDay, 50)), "max sessions per app per rolling 24h; 0 = unlimited")
	maxDeploysPerHour := flag.Int("max-deploys-per-hour", envInt("PLUMTREE_MAX_DEPLOYS_PER_HOUR", fileCfg.MaxDeploysPerHour), "max new deploy claims across the platform per rolling hour; 0 = unlimited")
	maxAppsPerOwner := flag.Int("max-apps-per-owner", envInt("PLUMTREE_MAX_APPS_PER_OWNER", fileCfg.MaxAppsPerOwner), "max apps a single owner may create; 0 = unlimited")
	deployClaimTTL := flag.Duration("deploy-claim-ttl", envDuration("PLUMTREE_DEPLOY_CLAIM_TTL", firstDuration(fileTTL, control.DeployClaimTTL)), "how long an unclaimed deploy may exist before garbage collection")
	anonPreview := flag.Bool("anonymous-preview", envBool("PLUMTREE_ANONYMOUS_PREVIEW", false), "allow running any deploy unclaimed at ssh preview-<deployID>@host, in the tightest sandbox")
	rateLimit := flag.Int("rate-limit", envInt("PLUMTREE_RATE_LIMIT", 20), "dashboard/API requests per second per client IP; 0 = unlimited")
	rateBurst := flag.Int("rate-burst", envInt("PLUMTREE_RATE_BURST", 40), "dashboard/API rate-limit burst per client IP")
	seedDemo := flag.Bool("seed-demo", false, "seed a demo owner/app for local UI development")
	buildURL := flag.String("build-url", env("PLUMTREE_BUILD_URL", ""), "remote build-worker URL; empty uses an in-process sandboxed builder")
	buildToken := flag.String("build-token", env("PLUMTREE_BUILD_TOKEN", ""), "shared token sent to the remote build-worker")
	buildDevRoot := flag.String("build-dev-root", env("PLUMTREE_DEV_ROOT", ""), "local repo root whose sdk/ and tui-runtime/ tie into the build workspace so the in-process builder resolves the unpublished SDK (local dev only)")
	flag.Parse()

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

	build := buildBackend(*buildURL, *buildToken, *buildDevRoot)

	handler := httpapi.NewWithConfig(httpapi.Config{
		Store:           store,
		Verifier:        verifier,
		AppOrigin:       *origin,
		DevToken:        *devToken,
		GatewayToken:    *gatewayToken,
		Build:           build,
		RateLimitPerSec: *rateLimit,
		RateLimitBurst:  *rateBurst,
		Limits: httpapi.LimitsInfo{
			MaxConcurrentSessions:   *maxSessions,
			MaxSessionsPerAppPerDay: *maxSessionsPerAppDay,
			MaxFramesPerSec:         runner.DefaultLimits.MaxFramesPerSec,
			MaxEventsPerSec:         runner.DefaultLimits.MaxEventsPerSec,
			SessionTimeout:          runner.DefaultLimits.SessionTimeout,
		},
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
		fmt.Printf("  pt claim             open the browser claim to take ownership (within %s)\n", *deployClaimTTL)
	} else {
		fmt.Println("  deploy is disabled — start with -dev-token TOKEN to allow `pt deploy`")
	}
	if *gatewayToken != "" {
		fmt.Println("  gateway API enabled at /internal/gateway (for a standalone ssh-gateway)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 2)

	httpServer := &http.Server{Addr: *addr, Handler: handler}
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		<-ctx.Done()
		_ = httpServer.Shutdown(context.Background())
	}()

	if *sshAddr != "" {
		gw := &gateway.Server{
			Backend:               gatewaybackend.New(store),
			Limits:                runner.DefaultLimits,
			MaxFPS:                *maxFPS,
			MaxConcurrentSessions: *maxSessions,
			StateDir:              stateDir(*stateFile),
			RunnerWorker:          *runnerWorker,
			Logf:                  func(f string, a ...any) { fmt.Fprintf(os.Stderr, "  "+f+"\n", a...) },
			Ready: func(a net.Addr) {
				host, port, _ := net.SplitHostPort(a.String())
				connectHost := gateway.HostFromListen(host)

				// connect renders the ssh command a user runs for a given handle,
				// matching whichever connection style is active (alias vs raw port).
				var connect func(handle string) string
				fmt.Println()
				switch {
				case *noSSHConfig:
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
