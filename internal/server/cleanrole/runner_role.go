package cleanrole

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/internal/runner"
	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	plumterminal "github.com/Ceinl/plumtree/internal/terminal"
)

type runnerComponent struct {
	projection serverconfig.RoleProjection
	out        io.Writer
	errOut     io.Writer
	environ    []string
	listener   net.Listener
	errors     chan error
	stopped    chan struct{}
	runErr     error
	mu         sync.Mutex
	once       sync.Once
}

func (c *runnerComponent) Start(ctx context.Context) error {
	cfg := c.projection.Config()
	network, address, ok := strings.Cut(cfg.Runtime.RunnerEndpoint, "://")
	if !ok || (network != "unix" && network != "tcp" && network != "tls") || address == "" {
		return errors.New("clean server: runner endpoint must be unix:///path, tcp://host:port, or tls://host:port")
	}
	if cfg.Runtime.Production && network == "tcp" {
		return fmt.Errorf("clean server: production refuses plain tcp:// runner endpoint %q; use unix:// or tls://", cfg.Runtime.RunnerEndpoint)
	}
	if network == "tcp" {
		_, _ = fmt.Fprintf(c.out, "warning: runner endpoint %s ships the broker token and session traffic unencrypted; prefer unix:// or tls://\n", cfg.Runtime.RunnerEndpoint)
	}
	var tlsConfig *tls.Config
	if network == "tls" {
		certFile, keyFile, err := c.tlsKeyPair()
		if err != nil {
			return err
		}
		if tlsConfig, err = runner.TLSListenerConfig(certFile, keyFile); err != nil {
			return err
		}
	}
	if network == "unix" {
		if err := prepareRunnerSocket(address); err != nil {
			return err
		}
	}
	if cfg.Runtime.RunnerScratchRoot != "" {
		if err := os.MkdirAll(cfg.Runtime.RunnerScratchRoot, 0o700); err != nil {
			return fmt.Errorf("clean server: runner scratch root: %w", err)
		}
	}
	var listenConfig net.ListenConfig
	listenNetwork := network
	if network == "tls" {
		listenNetwork = "tcp"
	}
	listener, err := listenConfig.Listen(ctx, listenNetwork, address)
	if err != nil {
		return fmt.Errorf("clean server: runner listen: %w", err)
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}
	c.listener = listener
	c.errors = make(chan error, 1)
	c.stopped = make(chan struct{})
	broker := &runner.Broker{
		WorkerPath: cfg.Runtime.RunnerWorker, Token: strings.TrimSpace(string(c.projection.Secret())),
		MaxSessions: cfg.Limits.MaxSessions, WorkerUIDBase: uint32(cfg.Runtime.WorkerUIDBase), ScratchRoot: cfg.Runtime.RunnerScratchRoot,
		Logf: plumterminal.EventFunc(c.eventOut(), plumterminal.ColorFor(c.eventOut())),
	}
	go func() {
		err := broker.Serve(ctx, listener)
		c.mu.Lock()
		c.runErr = err
		c.mu.Unlock()
		if err != nil {
			c.errors <- err
		}
		close(c.stopped)
	}()
	return nil
}

func (c *runnerComponent) Ready(context.Context) error {
	if c.listener == nil {
		return errors.New("clean server: runner role is not ready")
	}
	cfg := c.projection.Config()
	mode := "development"
	if cfg.Runtime.Production {
		mode = "production"
	}
	plumterminal.WriteRunnerSummary(c.out, plumterminal.RunnerSummary{
		Mode:     mode,
		Endpoint: cfg.Runtime.RunnerEndpoint,
		Worker:   cfg.Runtime.RunnerWorker,
		Scratch:  cfg.Runtime.RunnerScratchRoot,
		Next:     "connect the control plane to this runner",
	}, plumterminal.ColorFor(c.out))
	return nil
}

// eventOut defaults to io.Discard so zero-value components stay silent.
func (c *runnerComponent) eventOut() io.Writer {
	if c.errOut == nil {
		return io.Discard
	}
	return c.errOut
}

func (c *runnerComponent) Stop(ctx context.Context) error {
	c.once.Do(func() {
		if c.listener != nil {
			_ = c.listener.Close()
		}
	})
	select {
	case <-c.stopped:
		c.mu.Lock()
		err := c.runErr
		c.mu.Unlock()
		if errors.Is(err, net.ErrClosed) {
			err = nil
		}
		if network, address, ok := strings.Cut(c.projection.Config().Runtime.RunnerEndpoint, "://"); ok && network == "unix" {
			_ = os.Remove(address)
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// tlsKeyPair resolves the broker's TLS certificate and key from the
// PLUMTREE_RUNNER_TLS_CERT and PLUMTREE_RUNNER_TLS_KEY environment entries.
func (c *runnerComponent) tlsKeyPair() (certFile, keyFile string, err error) {
	env := environmentMap(c.environ)
	certFile = strings.TrimSpace(env["PLUMTREE_RUNNER_TLS_CERT"])
	keyFile = strings.TrimSpace(env["PLUMTREE_RUNNER_TLS_KEY"])
	if certFile == "" || keyFile == "" {
		return "", "", errors.New("clean server: tls:// runner endpoint requires PLUMTREE_RUNNER_TLS_CERT and PLUMTREE_RUNNER_TLS_KEY")
	}
	return certFile, keyFile, nil
}

func prepareRunnerSocket(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	connection, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		return errors.New("clean server: runner socket is already active")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("clean server: remove stale runner socket: %w", err)
	}
	return nil
}

func (c *runnerComponent) Errors() <-chan error { return c.errors }
