// Command plumtree-runner-broker is the production containment boundary for
// hostile WASM execution. It accepts authenticated gateway connections over a
// Unix socket and spawns one disposable runner-worker for each session.
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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Ceinl/plumtree/runner"
)

func main() {
	listen := flag.String("listen", env("PLUMTREE_RUNNER_LISTEN", "unix:///run/plumtree/runner.sock"), "broker endpoint (unix:///path or tcp://host:port)")
	worker := flag.String("worker", env("PLUMTREE_RUNNER_WORKER", "/usr/local/bin/plumtree-runner-worker"), "runner-worker executable")
	token := flag.String("token", env("PLUMTREE_RUNNER_TOKEN", ""), "shared gateway-to-runner token (required)")
	maxSessions := flag.Int("max-sessions", envInt("PLUMTREE_MAX_SESSIONS", 64), "maximum concurrent worker sessions; 0 = unlimited")
	workerUIDBase := flag.Uint("worker-uid-base", envUint("PLUMTREE_RUNNER_UID_BASE", 0), "first per-session worker UID/GID (production hardening)")
	scratchRoot := flag.String("scratch-root", env("PLUMTREE_RUNNER_SCRATCH", ""), "parent directory for private per-session scratch")
	socketUID := flag.Int("socket-uid", envInt("PLUMTREE_RUNNER_SOCKET_UID", -1), "owner UID for a Unix socket and its directory")
	socketGID := flag.Int("socket-gid", envInt("PLUMTREE_RUNNER_SOCKET_GID", -1), "owner GID for a Unix socket and its directory")
	flag.Parse()
	if *token == "" {
		log.Fatal("runner broker: -token is required")
	}
	ln, cleanup, err := listenEndpoint(*listen, *socketUID, *socketGID)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("runner broker: listening on %s\n", *listen)
	b := &runner.Broker{
		WorkerPath:    *worker,
		Token:         *token,
		MaxSessions:   *maxSessions,
		WorkerUIDBase: uint32(*workerUIDBase),
		ScratchRoot:   *scratchRoot,
		Logf:          func(f string, a ...any) { log.Printf(f, a...) },
	}
	if err := b.Serve(ctx, ln); err != nil {
		log.Fatal(err)
	}
}

func listenEndpoint(endpoint string, socketUID, socketGID int) (net.Listener, func(), error) {
	network, address, ok := strings.Cut(endpoint, "://")
	if !ok || (network != "unix" && network != "tcp") || address == "" {
		return nil, nil, fmt.Errorf("runner broker: invalid endpoint %q (want unix:///path or tcp://host:port)", endpoint)
	}
	cleanup := func() {}
	if network == "unix" {
		if err := os.MkdirAll(filepath.Dir(address), 0o700); err != nil {
			return nil, nil, err
		}
		if socketUID >= 0 || socketGID >= 0 {
			if err := os.Chown(filepath.Dir(address), socketUID, socketGID); err != nil {
				return nil, nil, err
			}
		}
		if info, err := os.Lstat(address); err == nil {
			if info.Mode()&os.ModeSocket == 0 {
				return nil, nil, fmt.Errorf("runner broker: refusing to replace non-socket %q", address)
			}
			if err := os.Remove(address); err != nil {
				return nil, nil, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		cleanup = func() { _ = os.Remove(address) }
	}
	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, nil, err
	}
	if network == "unix" {
		// Set permissions while the broker still owns the socket. After Chown,
		// chmod would require CAP_FOWNER, which production intentionally drops.
		if err := os.Chmod(address, 0o600); err != nil {
			_ = ln.Close()
			cleanup()
			return nil, nil, err
		}
		if socketUID >= 0 || socketGID >= 0 {
			if err := os.Chown(address, socketUID, socketGID); err != nil {
				_ = ln.Close()
				cleanup()
				return nil, nil, err
			}
		}
	}
	return ln, func() { _ = ln.Close(); cleanup() }, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return fallback
}

func envUint(key string, fallback uint) uint {
	if value := os.Getenv(key); value != "" {
		if n, err := strconv.ParseUint(value, 10, 32); err == nil {
			return uint(n)
		}
	}
	return fallback
}
