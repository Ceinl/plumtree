// Command build-worker runs the sandboxed source-to-WASM build service as its
// own process, separate from the control plane. It exposes POST /build and
// holds no app metadata, auth state, or secrets.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	buildworker "github.com/Ceinl/plumtree/build-worker"
)

const (
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 5 * time.Minute
	httpIdleTimeout       = 1 * time.Minute
	httpShutdownTimeout   = 10 * time.Second
)

func main() {
	addr := flag.String("addr", env("PLUMTREE_BUILD_ADDR", ":8090"), "HTTP listen address")
	token := flag.String("token", env("PLUMTREE_BUILD_TOKEN", ""), "shared token required from callers; empty disables auth")
	goBin := flag.String("go", env("PLUMTREE_BUILD_GO", "go"), "go toolchain binary")
	goProxy := flag.String("goproxy", env("GOPROXY_BUILD", "off"), "GOPROXY for builds; 'off' means no network")
	workRoot := flag.String("work-root", env("PLUMTREE_BUILD_WORK_ROOT", ""), "parent dir for build sandboxes; empty uses the OS temp dir")
	workspace := flag.String("workspace-modules", env("PLUMTREE_BUILD_WORKSPACE", ""), "comma-separated local module dirs (e.g. a baked-in sdk and tui-runtime) tied into each build workspace so the unpublished SDK resolves without a registry")
	timeout := flag.Duration("timeout", durEnv("PLUMTREE_BUILD_TIMEOUT", 90*time.Second), "per-build wall-clock limit")
	maxSource := flag.Int64("max-source-bytes", int64Env("PLUMTREE_MAX_SOURCE_BYTES", 8<<20), "max uploaded source archive size")
	maxMemory := flag.Int64("max-memory-bytes", int64Env("PLUMTREE_MAX_MEMORY_BYTES", 2<<30), "address-space limit for the build process (Linux); negative disables")
	maxConcurrent := flag.Int("max-concurrent-builds", intEnv("PLUMTREE_MAX_CONCURRENT_BUILDS", 2), "maximum simultaneous builds; 0 = unlimited")
	maxQueued := flag.Int("max-queued-builds", intEnv("PLUMTREE_MAX_QUEUED_BUILDS", 8), "maximum builds waiting for a worker; 0 rejects when busy")
	production := flag.Bool("production", boolEnv("PLUMTREE_PRODUCTION", false), "enable production safety checks")
	ackUnlimited := flag.Bool("acknowledge-unlimited-limits", boolEnv("PLUMTREE_ACKNOWLEDGE_UNLIMITED_LIMITS", false), "allow production startup with critical limits disabled")
	flag.Parse()
	if err := validateProductionLimits(*production, *ackUnlimited, *timeout, *maxSource, *maxMemory, *maxConcurrent); err != nil {
		log.Fatal(err)
	}

	builder := buildworker.NewBuilder(buildworker.Config{
		GoBin:            *goBin,
		WorkRoot:         *workRoot,
		GoProxy:          *goProxy,
		Timeout:          *timeout,
		MaxSourceBytes:   *maxSource,
		MaxMemoryBytes:   *maxMemory,
		WorkspaceModules: splitList(*workspace),
	})
	svc := buildworker.NewServiceWithLimits(builder, *token, *maxConcurrent, *maxQueued)

	srv := newHTTPServer(*addr, svc.Handler())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	fmt.Printf("build-worker listening on %s (goproxy=%s, timeout=%s)\n", *addr, *goProxy, *timeout)
	if *token == "" {
		fmt.Println("warning: build token auth is disabled")
	}
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
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

func validateProductionLimits(production, acknowledged bool, timeout time.Duration, maxSource, maxMemory int64, maxConcurrent int) error {
	if !production || acknowledged {
		return nil
	}
	var unlimited []string
	if timeout <= 0 {
		unlimited = append(unlimited, "timeout")
	}
	if maxSource <= 0 {
		unlimited = append(unlimited, "max-source-bytes")
	}
	if maxMemory < 0 {
		unlimited = append(unlimited, "max-memory-bytes")
	}
	if maxConcurrent <= 0 {
		unlimited = append(unlimited, "max-concurrent-builds")
	}
	if len(unlimited) == 0 {
		return nil
	}
	return fmt.Errorf("build-worker: refusing production startup with unlimited critical limits: %s (set PLUMTREE_ACKNOWLEDGE_UNLIMITED_LIMITS=true to acknowledge)", strings.Join(unlimited, ", "))
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func durEnv(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func int64Env(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func boolEnv(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
