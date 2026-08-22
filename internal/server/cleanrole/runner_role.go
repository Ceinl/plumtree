package cleanrole

import (
	"context"
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
)

type runnerComponent struct {
	projection serverconfig.RoleProjection
	out        io.Writer
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
	if !ok || (network != "unix" && network != "tcp") || address == "" {
		return errors.New("clean server: runner endpoint must be unix:///path or tcp://host:port")
	}
	if cfg.Runtime.Production && network != "unix" {
		return errors.New("clean server: production runner endpoint must use a Unix socket")
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
	listener, err := listenConfig.Listen(ctx, network, address)
	if err != nil {
		return fmt.Errorf("clean server: runner listen: %w", err)
	}
	c.listener = listener
	c.errors = make(chan error, 1)
	c.stopped = make(chan struct{})
	broker := &runner.Broker{
		WorkerPath: cfg.Runtime.RunnerWorker, Token: strings.TrimSpace(string(c.projection.Secret())),
		MaxSessions: cfg.Limits.MaxSessions, WorkerUIDBase: uint32(cfg.Runtime.WorkerUIDBase), ScratchRoot: cfg.Runtime.RunnerScratchRoot,
		Logf: func(format string, args ...any) { _, _ = fmt.Fprintf(c.out, format+"\n", args...) },
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
	_, _ = fmt.Fprintf(c.out, "plumtree runner ready on %s\n", c.projection.Config().Runtime.RunnerEndpoint)
	return nil
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
