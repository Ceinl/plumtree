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
	"syscall"
	"time"

	buildworker "github.com/Ceinl/plumtree/build-worker"
)

func main() {
	addr := flag.String("addr", env("PLUMTREE_BUILD_ADDR", ":8090"), "HTTP listen address")
	token := flag.String("token", env("PLUMTREE_BUILD_TOKEN", ""), "shared token required from callers; empty disables auth")
	goBin := flag.String("go", env("PLUMTREE_BUILD_GO", "go"), "go toolchain binary")
	goProxy := flag.String("goproxy", env("GOPROXY_BUILD", "off"), "GOPROXY for builds; 'off' means no network")
	workRoot := flag.String("work-root", env("PLUMTREE_BUILD_WORK_ROOT", ""), "parent dir for build sandboxes; empty uses the OS temp dir")
	timeout := flag.Duration("timeout", durEnv("PLUMTREE_BUILD_TIMEOUT", 90*time.Second), "per-build wall-clock limit")
	maxSource := flag.Int64("max-source-bytes", 8<<20, "max uploaded source archive size")
	flag.Parse()

	builder := buildworker.NewBuilder(buildworker.Config{
		GoBin:          *goBin,
		WorkRoot:       *workRoot,
		GoProxy:        *goProxy,
		Timeout:        *timeout,
		MaxSourceBytes: *maxSource,
	})
	svc := buildworker.NewService(builder, *token)

	srv := &http.Server{Addr: *addr, Handler: svc.Handler()}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	fmt.Printf("build-worker listening on %s (goproxy=%s, timeout=%s)\n", *addr, *goProxy, *timeout)
	if *token == "" {
		fmt.Println("warning: build token auth is disabled")
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
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
