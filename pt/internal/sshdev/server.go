// Package sshdev serves a Plumtree app over SSH for local development. It is a
// thin, single-app stand-in for the production SSH gateway: every connection
// that requests a shell gets a fresh wazero session (via runner) wired to the
// SSH channel — keystrokes in, rendered frames out — so `pt dev --ssh` lets you
// run the app exactly the way users will: `ssh -p <port> localhost`.
//
// Dev-only simplifications: anonymous auth (no key required), a stable local
// dev host key, and one app per server. Real auth, `<owner>/<app>` routing, and
// quotas belong to the gateway/control-plane phases.
package sshdev

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/Ceinl/plumtree/runner"
	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
	"github.com/Ceinl/plumtree/tui-runtime/terminal"
	"golang.org/x/crypto/ssh"
)

// Server serves one app over SSH.
type Server struct {
	Wasm    []byte
	Runner  *runner.Runner
	Limits  runner.Limits
	Caps    runner.Capabilities // host capabilities shared across all sessions
	AppType string              // "tui" or "cli"
	AppName string
	MaxFPS  int
	Logf    func(format string, args ...any)
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// ListenAndServe listens on addr until ctx is cancelled, serving each
// connection in its own goroutine. It returns the resolved listen address via
// the ready callback (handy when addr uses port 0).
func (s *Server) ListenAndServe(ctx context.Context, addr string, ready func(net.Addr)) error {
	if s.Runner == nil {
		s.Runner = runner.New()
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true} // dev: anyone on localhost may connect
	signer, err := devHostKey()
	if err != nil {
		return err
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	if ready != nil {
		ready(ln.Addr())
	}

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // shutting down
			}
			return err
		}
		go s.handleConn(ctx, conn, cfg)
	}
}

func (s *Server) handleConn(ctx context.Context, nConn net.Conn, cfg *ssh.ServerConfig) {
	defer nConn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(nConn, cfg)
	if err != nil {
		s.logf("ssh handshake from %s failed: %v", nConn.RemoteAddr(), err)
		return
	}
	defer sshConn.Close()
	s.logf("connected: user=%q from %s", sshConn.User(), nConn.RemoteAddr())
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			s.logf("accept channel: %v", err)
			continue
		}
		go s.handleSession(ctx, ch, chReqs)
	}
}

// handleSession processes channel requests and, once a shell/exec is asked for,
// runs the app against the channel. Terminal size starts at the pty-req value
// and tracks window-change events.
func (s *Server) handleSession(ctx context.Context, ch ssh.Channel, reqs <-chan *ssh.Request) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu      sync.Mutex
		w, h    = 80, 24
		started bool
	)
	winch := make(chan os.Signal, 1)
	size := func() (int, int) { mu.Lock(); defer mu.Unlock(); return w, h }

	for req := range reqs {
		switch req.Type {
		case "pty-req":
			var p ptyRequest
			if err := ssh.Unmarshal(req.Payload, &p); err == nil {
				mu.Lock()
				w, h = int(p.Columns), int(p.Rows)
				mu.Unlock()
			}
			req.Reply(true, nil)

		case "window-change":
			var p windowChange
			if err := ssh.Unmarshal(req.Payload, &p); err == nil {
				mu.Lock()
				w, h = int(p.Columns), int(p.Rows)
				mu.Unlock()
				select {
				// TTYSource treats this channel as a resize notification; the
				// concrete signal value is irrelevant. os.Interrupt keeps this
				// development server portable to Windows as well.
				case winch <- os.Interrupt:
				default:
				}
			}

		case "shell", "exec":
			req.Reply(true, nil)
			if started {
				continue
			}
			started = true
			go func() {
				s.runSession(ctx, ch, size, winch)
				ch.Close()
				cancel()
			}()

		case "env":
			req.Reply(true, nil)

		default:
			req.Reply(false, nil)
		}
	}
	cancel()
}

func (s *Server) runSession(ctx context.Context, ch ssh.Channel, size func() (int, int), winch chan os.Signal) {
	if s.AppType == "cli" {
		if err := s.Runner.RunCLI(ctx, s.Wasm, s.Limits, s.Caps, nil, ch); err != nil {
			fmt.Fprintf(ch.Stderr(), "app error: %v\r\n", err)
		}
		return
	}

	w, h := size()
	if w <= 0 || h <= 0 {
		w, h = 80, 24
	}

	// Set up the client's terminal (alt screen, hidden cursor) and tear it down
	// afterward. Output is host-generated; the guest never writes ANSI.
	io.WriteString(ch, terminal.HIDE_CURSOR+terminal.OPEN_ALT+terminal.ENABLE_MOUSE+terminal.CLEAR_SCREEN+terminal.MOVE_CURSOR)
	defer io.WriteString(ch, terminal.DISABLE_MOUSE+terminal.SHOW_CURSOR+terminal.CLOSE_ALT)

	src := &runner.TTYSource{
		Keys:    keyboard.ListenReader(ctx, ch),
		Winch:   winch,
		Refresh: runner.DefaultRefresh,
		Size:    size,
	}
	sink := runner.NewTTYSinkWriter(w, h, s.MaxFPS, ch)

	var logs bytes.Buffer
	err := s.Runner.Run(ctx, s.Wasm, s.Limits, s.Caps, src, sink, &logs)
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		// Clean exit or normal client disconnect — nothing to report.
	default:
		s.logf("session error: %v", err)
	}
}

// devHostKey returns a stable host key, persisted under the user config dir so
// it does not change between runs — clients then trust it once instead of
// needing StrictHostKeyChecking=no on every connect. Falls back to an ephemeral
// key if the config dir is unavailable.
func devHostKey() (ssh.Signer, error) {
	gen := func() (ssh.Signer, error) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		return ssh.NewSignerFromSigner(priv)
	}

	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return gen()
	}
	path := filepath.Join(cfgDir, "plumtree", "dev_host_ed25519")

	if b, err := os.ReadFile(path); err == nil {
		if signer, err := ssh.ParsePrivateKey(b); err == nil {
			return signer, nil
		}
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if der, err := x509.MarshalPKCS8PrivateKey(priv); err == nil {
		blk := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			_ = os.WriteFile(path, blk, 0o600)
		}
	}
	return ssh.NewSignerFromSigner(priv)
}

// ptyRequest is the SSH "pty-req" payload.
type ptyRequest struct {
	Term              string
	Columns, Rows     uint32
	WidthPx, HeightPx uint32
	Modes             string
}

// windowChange is the SSH "window-change" payload.
type windowChange struct {
	Columns, Rows     uint32
	WidthPx, HeightPx uint32
}
