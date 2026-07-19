// Command ssh-gateway runs the Plumtree SSH gateway as a standalone process,
// using a remote control plane as its backend over the gateway API. It owns the
// SSH front end and the per-session WASM sandbox; the control plane owns app
// resolution, session accounting, and capability config.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	if err := validateProductionLimits(flags); err != nil {
		log.Fatal(err)
	}

	gw := &gateway.Server{
		Backend:               httpbackend.New(flags.controlURL, flags.gatewayToken),
		Limits:                runner.DefaultLimits,
		MaxFPS:                flags.maxFPS,
		MaxConcurrentSessions: flags.maxSessions,
		HandshakeTimeout:      flags.handshakeTimeout,
		IdleTimeout:           flags.idleTimeout,
		MaxConnections:        flags.maxConnections,
		MaxConnectionsPerIP:   flags.maxConnectionsPerIP,
		StateDir:              flags.stateDir,
		RunnerWorker:          flags.runnerWorker,
		RunnerEndpoint:        flags.runnerEndpoint,
		RunnerToken:           flags.runnerToken,
		Logf:                  func(f string, a ...any) { fmt.Fprintf(os.Stderr, "  "+f+"\n", a...) },
		Ready: func(a net.Addr) {
			host, port, _ := net.SplitHostPort(a.String())
			fmt.Printf("ssh gateway: listening on %s:%s\n", gateway.HostFromListen(host), port)
			fmt.Printf("control plane: %s\n", flags.controlURL)
		},
	}

	fmt.Printf("starting ssh-gateway on %s\n", flags.sshAddr)
	if flags.runnerEndpoint != "" {
		fmt.Printf("runner isolation: remote broker %s\n", flags.runnerEndpoint)
	} else if flags.runnerWorker != "" {
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
	controlURL          string
	gatewayToken        string
	sshAddr             string
	stateDir            string
	runnerWorker        string
	runnerEndpoint      string
	runnerToken         string
	maxFPS              int
	maxSessions         int
	handshakeTimeout    time.Duration
	idleTimeout         time.Duration
	maxConnections      int
	maxConnectionsPerIP int
	production          bool
	ackUnlimited        bool
}

func parseFlags() config {
	controlURL := flag.String("control-url", env("PLUMTREE_CONTROL_URL", ""), "control-plane base URL the gateway uses as its backend (required)")
	gatewayToken := flag.String("gateway-token", env("PLUMTREE_GATEWAY_TOKEN", ""), "shared token sent to the control-plane gateway API (required)")
	sshAddr := flag.String("ssh-addr", env("PLUMTREE_SSH_ADDR", "0.0.0.0:2222"), "SSH listen address")
	stateDir := flag.String("state-dir", env("PLUMTREE_STATE_DIR", ""), "directory for per-app KV stores (under state-dir/kv); empty disables KV")
	runnerWorker := flag.String("runner-worker", env("PLUMTREE_RUNNER_WORKER", ""), "path to the plumtree-runner-worker binary; set to isolate each app session in a separate process")
	runnerEndpoint := flag.String("runner-endpoint", env("PLUMTREE_RUNNER_ENDPOINT", ""), "remote runner broker endpoint (production: unix:///path)")
	runnerToken := flag.String("runner-token", env("PLUMTREE_RUNNER_TOKEN", ""), "shared token for the remote runner broker")
	maxFPS := flag.Int("max-fps", envInt("PLUMTREE_MAX_FPS", 60), "SSH repaint cap")
	maxSessions := flag.Int("max-sessions", envInt("PLUMTREE_MAX_SESSIONS", gateway.DefaultMaxConcurrentSessions), "max concurrent SSH sessions on this gateway; 0 = unlimited")
	handshakeTimeout := flag.Duration("handshake-timeout", envDuration("PLUMTREE_SSH_HANDSHAKE_TIMEOUT", gateway.DefaultHandshakeTimeout), "maximum time allowed for an SSH handshake; negative disables")
	idleTimeout := flag.Duration("idle-timeout", envDuration("PLUMTREE_SSH_IDLE_TIMEOUT", gateway.DefaultIdleTimeout), "disconnect an established SSH connection after this much network inactivity; negative disables")
	maxConnections := flag.Int("max-connections", envInt("PLUMTREE_MAX_CONNECTIONS", gateway.DefaultMaxConnections), "maximum admitted TCP connections; negative disables")
	maxConnectionsPerIP := flag.Int("max-connections-per-ip", envInt("PLUMTREE_MAX_CONNECTIONS_PER_IP", gateway.DefaultMaxConnectionsPerIP), "maximum admitted TCP connections per client IP; negative disables")
	production := flag.Bool("production", envBool("PLUMTREE_PRODUCTION", false), "enable production safety checks")
	ackUnlimited := flag.Bool("acknowledge-unlimited-limits", envBool("PLUMTREE_ACKNOWLEDGE_UNLIMITED_LIMITS", false), "allow production startup with critical limits disabled")
	flag.Parse()
	return config{
		controlURL:          *controlURL,
		gatewayToken:        *gatewayToken,
		sshAddr:             *sshAddr,
		stateDir:            *stateDir,
		runnerWorker:        *runnerWorker,
		runnerEndpoint:      *runnerEndpoint,
		runnerToken:         *runnerToken,
		maxFPS:              *maxFPS,
		maxSessions:         *maxSessions,
		handshakeTimeout:    *handshakeTimeout,
		idleTimeout:         *idleTimeout,
		maxConnections:      *maxConnections,
		maxConnectionsPerIP: *maxConnectionsPerIP,
		production:          *production,
		ackUnlimited:        *ackUnlimited,
	}
}

func validateProductionLimits(cfg config) error {
	if cfg.runnerEndpoint != "" && cfg.runnerWorker != "" {
		return errors.New("ssh-gateway: configure either runner-endpoint or runner-worker, not both")
	}
	if cfg.runnerEndpoint != "" && cfg.runnerToken == "" {
		return errors.New("ssh-gateway: runner-token is required with runner-endpoint")
	}
	if cfg.production && cfg.runnerEndpoint == "" {
		return errors.New("ssh-gateway: production requires a remote runner-endpoint; a local subprocess does not contain native runtime escape")
	}
	if cfg.production && !strings.HasPrefix(cfg.runnerEndpoint, "unix://") {
		return errors.New("ssh-gateway: production runner-endpoint must use an authenticated Unix socket")
	}
	if !cfg.production || cfg.ackUnlimited {
		return nil
	}
	var unlimited []string
	if cfg.maxSessions <= 0 {
		unlimited = append(unlimited, "max-sessions")
	}
	if cfg.maxConnections < 0 {
		unlimited = append(unlimited, "max-connections")
	}
	if cfg.maxConnectionsPerIP < 0 {
		unlimited = append(unlimited, "max-connections-per-ip")
	}
	if cfg.handshakeTimeout < 0 {
		unlimited = append(unlimited, "handshake-timeout")
	}
	if cfg.idleTimeout < 0 {
		unlimited = append(unlimited, "idle-timeout")
	}
	if len(unlimited) == 0 {
		return nil
	}
	return fmt.Errorf("ssh-gateway: refusing production startup with unlimited critical limits: %s (set PLUMTREE_ACKNOWLEDGE_UNLIMITED_LIMITS=true to acknowledge)", strings.Join(unlimited, ", "))
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

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
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
