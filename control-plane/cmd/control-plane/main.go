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

	buildworker "github.com/Ceinl/plumtree/build-worker"
	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/control-plane/internal/httpapi"
	"github.com/Ceinl/plumtree/control-plane/internal/sshgateway"
	"github.com/Ceinl/plumtree/runner"
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
	addr := flag.String("addr", env("PLUMTREE_ADDR", ":8080"), "HTTP listen address")
	origin := flag.String("origin", env("PLUMTREE_PUBLIC_ORIGIN", "http://localhost:8080"), "public dashboard origin")
	shooBase := flag.String("shoo-base-url", env("SHOO_BASE_URL", shoo.DefaultBaseURL), "Shoo base URL")
	devToken := flag.String("dev-token", env("PLUMTREE_DEV_TOKEN", ""), "enable local dev deploy API with this token")
	stateFile := flag.String("state-file", env("PLUMTREE_STATE_FILE", defaultStateFile()), "persistent state file; empty disables persistence")
	blobDir := flag.String("blob-dir", env("PLUMTREE_BLOB_DIR", ""), "directory for durable WASM artifact storage; empty keeps artifacts in the state file")
	sshAddr := flag.String("ssh-addr", env("PLUMTREE_SSH_ADDR", "127.0.0.1:2222"), "SSH gateway listen address; empty disables SSH")
	sshHost := flag.String("ssh-host", env("PLUMTREE_SSH_HOST", "plumtree.dev"), "local SSH host alias written to ~/.ssh/config")
	noSSHConfig := flag.Bool("no-ssh-config", false, "do not update ~/.ssh/config for the local SSH gateway")
	maxFPS := flag.Int("max-fps", 60, "SSH repaint cap")
	maxSessions := flag.Int("max-sessions", envInt("PLUMTREE_MAX_SESSIONS", 0), "max concurrent SSH sessions on this runner; 0 = unlimited")
	maxSessionsPerAppDay := flag.Int("max-sessions-per-app-day", envInt("PLUMTREE_MAX_SESSIONS_PER_APP_DAY", 50), "max sessions per app per rolling 24h; 0 = unlimited")
	maxDeploysPerHour := flag.Int("max-deploys-per-hour", envInt("PLUMTREE_MAX_DEPLOYS_PER_HOUR", 0), "max new deploy claims across the platform per rolling hour; 0 = unlimited")
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
	fmt.Printf("control-plane listening on %s\n", *addr)
	fmt.Printf("dashboard: %s/dashboard\n", strings.TrimRight(*origin, "/"))
	if *buildURL != "" {
		fmt.Printf("build worker: %s\n", *buildURL)
	} else {
		fmt.Println("build worker: in-process sandbox")
	}
	if *devToken != "" {
		fmt.Println("local dev deploy API: enabled")
	}
	if *stateFile != "" {
		fmt.Printf("persistent state: %s\n", *stateFile)
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
		gw := &sshgateway.Server{
			Store:                 store,
			Limits:                runner.DefaultLimits,
			MaxFPS:                *maxFPS,
			MaxConcurrentSessions: *maxSessions,
			StateDir:              stateDir(*stateFile),
			Logf:                  func(f string, a ...any) { fmt.Fprintf(os.Stderr, "  "+f+"\n", a...) },
			Ready: func(a net.Addr) {
				host, port, _ := net.SplitHostPort(a.String())
				connectHost := sshgateway.HostFromListen(host)
				if *noSSHConfig {
					fmt.Printf("ssh gateway: listening on %s:%s\n", connectHost, port)
					fmt.Printf("connect deployed apps with: ssh -p %s -o HostKeyAlias=plumtree-dev -o StrictHostKeyChecking=accept-new <app>@%s\n", port, connectHost)
				} else if path, err := installDevSSHConfig(*sshHost, connectHost, port); err == nil {
					fmt.Printf("ssh gateway: %s maps %s -> %s:%s\n", path, *sshHost, connectHost, port)
					fmt.Printf("connect deployed apps with: ssh <app>@%s\n", *sshHost)
				} else {
					fmt.Fprintf(os.Stderr, "ssh config update failed: %v\n", err)
					fmt.Printf("connect deployed apps with: ssh -p %s <app>@%s\n", port, connectHost)
				}
			},
		}
		go func() {
			if err := gw.ListenAndServe(ctx, *sshAddr); err != nil {
				errCh <- err
			}
		}()
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
	app, err := store.EnsureApp(control.AppInput{OwnerID: owner.ID, Name: "counter", Visibility: control.VisibilityPublic})
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
