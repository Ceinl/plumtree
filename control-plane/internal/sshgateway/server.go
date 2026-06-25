// Package sshgateway serves deployed Plumtree apps over SSH for the local
// all-in-one control-plane prototype.
package sshgateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/runner"
)

type Server struct {
	Store  *control.Store
	Runner *runner.Runner
	Limits runner.Limits
	MaxFPS int
	// StateDir is where per-app KV stores are persisted (under StateDir/kv).
	// Empty disables KV: apps still run but their storage is unavailable.
	StateDir string
	// MaxConcurrentSessions caps how many sessions run on this runner at once
	// (the runner-wide concurrency quota). 0 means unlimited. Per-owner limits
	// are enforced separately by the store's Quotas.MaxSessions.
	MaxConcurrentSessions int
	Logf                  func(format string, args ...any)
	Ready                 func(net.Addr)

	sessions *sessionRegistry
	slots    chan struct{} // counting semaphore; nil when unlimited

	kvMu     sync.Mutex
	kvStores map[string]runner.Store // app ID -> shared store

	busMu   sync.Mutex
	busById map[string]*runner.MemBus // app ID -> shared pub/sub bus
}

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if s.Store == nil {
		return errors.New("sshgateway: store is required")
	}
	if s.Runner == nil {
		s.Runner = runner.New()
	}
	if s.sessions == nil {
		s.sessions = newSessionRegistry()
	}
	if s.slots == nil && s.MaxConcurrentSessions > 0 {
		s.slots = make(chan struct{}, s.MaxConcurrentSessions)
	}
	// Accept every connection (anonymous run is supported), but capture the
	// offered public key's fingerprint so the Auth capability can identify the
	// user. Clients with a key authenticate via publickey (key recorded); clients
	// without one fall through to NoClientAuth.
	cfg := &ssh.ServerConfig{
		NoClientAuth: true,
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{
				Extensions: map[string]string{"pubkey-fp": ssh.FingerprintSHA256(key)},
			}, nil
		},
	}
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
	if s.Ready != nil {
		s.Ready(ln.Addr())
	}

	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handleConn(ctx, conn, cfg)
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
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
	go ssh.DiscardRequests(reqs)

	app, deploy, artifact, wasm, err := s.Store.ResolveRunnable(sshConn.User())
	if err != nil {
		s.logf("resolve %q from %s failed: %v", sshConn.User(), nConn.RemoteAddr(), err)
		msg := fmt.Sprintf("app %q is not available", sshConn.User())
		if errors.Is(err, control.ErrSuspended) {
			msg = fmt.Sprintf("app %q is temporarily unavailable (suspended)", sshConn.User())
		}
		discardChannels(chans, msg)
		return
	}
	appType := artifact.BuildMetadata["app_type"]
	if appType == "" {
		appType = "tui"
	}
	identity := identityFromConn(sshConn)
	s.logf("connected: user=%q app=%q deploy=%q id=%q from %s", sshConn.User(), app.Name, app.ActiveDeployID, identity.User, nConn.RemoteAddr())

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
		go s.handleSession(ctx, ch, chReqs, app, deploy, wasm, appType, identity)
	}
}

// identityFromConn derives the session identity from the SSH connection: the
// offered key's fingerprint when present, otherwise an ephemeral id from the
// connection's session id. The gateway does not yet match keys to owners, so the
// identity is never marked Authenticated here.
func identityFromConn(c *ssh.ServerConn) runner.Identity {
	if c.Permissions != nil {
		if fp := c.Permissions.Extensions["pubkey-fp"]; fp != "" {
			return runner.Identity{User: fp}
		}
	}
	sid := c.SessionID()
	if len(sid) > 8 {
		sid = sid[:8]
	}
	return runner.Identity{User: "anon-" + hex.EncodeToString(sid)}
}

func discardChannels(chans <-chan ssh.NewChannel, message string) {
	for newCh := range chans {
		ch, reqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go ssh.DiscardRequests(reqs)
		fmt.Fprintf(ch.Stderr(), "%s\r\n", message)
		ch.Close()
	}
}

func HostFromListen(listenHost string) string {
	switch listenHost {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return strings.Trim(listenHost, "[]")
	}
}
