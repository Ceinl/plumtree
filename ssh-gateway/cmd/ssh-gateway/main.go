// Command ssh-gateway runs the Plumtree SSH gateway as a standalone process,
// using a remote control plane as its backend over the gateway API. It owns the
// SSH front end and the per-session WASM sandbox; the control plane owns app
// resolution, session accounting, and capability config.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/Ceinl/plumtree/runner"
	"github.com/Ceinl/plumtree/ssh-gateway/gateway"
	"github.com/Ceinl/plumtree/ssh-gateway/httpbackend"
)

func main() {
	flags := parseFlags()
	if flags.controlURL == "" {
		log.Fatal("ssh-gateway: -control-url is required")
	}
	if flags.gatewayToken == "" {
		log.Fatal("ssh-gateway: -gateway-token is required")
	}

	gw := &gateway.Server{
		Backend:               httpbackend.New(flags.controlURL, flags.gatewayToken),
		Limits:                runner.DefaultLimits,
		MaxFPS:                flags.maxFPS,
		MaxConcurrentSessions: flags.maxSessions,
		StateDir:              flags.stateDir,
		RunnerWorker:          flags.runnerWorker,
		Logf:                  func(f string, a ...any) { fmt.Fprintf(os.Stderr, "  "+f+"\n", a...) },
		Ready: func(a net.Addr) {
			host, port, _ := net.SplitHostPort(a.String())
			fmt.Printf("ssh gateway: listening on %s:%s\n", gateway.HostFromListen(host), port)
			fmt.Printf("control plane: %s\n", flags.controlURL)
		},
	}

	fmt.Printf("starting ssh-gateway on %s\n", flags.sshAddr)
	if flags.runnerWorker != "" {
		fmt.Printf("runner isolation: %s\n", flags.runnerWorker)
	} else {
		fmt.Println("runner isolation: in-process sandbox")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := gw.ListenAndServe(ctx, flags.sshAddr); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	controlURL   string
	gatewayToken string
	sshAddr      string
	stateDir     string
	runnerWorker string
	maxFPS       int
	maxSessions  int
}

func parseFlags() config {
	controlURL := flag.String("control-url", env("PLUMTREE_CONTROL_URL", ""), "control-plane base URL the gateway uses as its backend (required)")
	gatewayToken := flag.String("gateway-token", env("PLUMTREE_GATEWAY_TOKEN", ""), "shared token sent to the control-plane gateway API (required)")
	sshAddr := flag.String("ssh-addr", env("PLUMTREE_SSH_ADDR", "0.0.0.0:2222"), "SSH listen address")
	stateDir := flag.String("state-dir", env("PLUMTREE_STATE_DIR", ""), "directory for per-app KV stores (under state-dir/kv); empty disables KV")
	runnerWorker := flag.String("runner-worker", env("PLUMTREE_RUNNER_WORKER", ""), "path to the plumtree-runner-worker binary; set to isolate each TUI session in a separate process")
	maxFPS := flag.Int("max-fps", envInt("PLUMTREE_MAX_FPS", 60), "SSH repaint cap")
	maxSessions := flag.Int("max-sessions", envInt("PLUMTREE_MAX_SESSIONS", 0), "max concurrent SSH sessions on this gateway; 0 = unlimited")
	flag.Parse()
	return config{
		controlURL:   *controlURL,
		gatewayToken: *gatewayToken,
		sshAddr:      *sshAddr,
		stateDir:     *stateDir,
		runnerWorker: *runnerWorker,
		maxFPS:       *maxFPS,
		maxSessions:  *maxSessions,
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
