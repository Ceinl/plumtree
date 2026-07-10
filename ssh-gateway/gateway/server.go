// Package gateway serves deployed Plumtree apps over SSH. It owns the SSH front
// end, the PTY/session lifecycle, and the per-session WASM sandbox, delegating
// all authoritative platform state to a Backend. It runs either embedded in the
// control plane (in-process Backend) or as its own deployable (HTTP Backend).
package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/runner"
	"golang.org/x/crypto/ssh"
)

type Server struct {
	// Backend is the port to the control plane (required).
	Backend Backend
	Runner  *runner.Runner
	Limits  runner.Limits
	MaxFPS  int
	// HostKey signs the SSH host key. When nil, a persistent dev host key under
	// the OS config dir is loaded or generated.
	HostKey ssh.Signer
	// StateDir is where per-app KV stores are persisted (under StateDir/kv).
	// Empty disables KV: apps still run but their storage is unavailable.
	StateDir string
	// RunnerWorker, when set, is the path to the runner-worker binary; all app
	// sessions then run the WASM sandbox in a separate process isolated from the
	// gateway. Empty runs the sandbox in-process (shared compile cache).
	RunnerWorker string
	// MaxConcurrentSessions caps how many sessions run on this gateway at once
	// (the runner-wide concurrency quota). 0 means unlimited. Per-owner limits
	// are enforced separately by the Backend's session accounting.
	MaxConcurrentSessions int
	// HandshakeTimeout bounds the SSH handshake. IdleTimeout is an
	// activity-based deadline for established SSH connections. Zero selects the
	// secure defaults; a negative value disables the corresponding deadline.
	HandshakeTimeout time.Duration
	IdleTimeout      time.Duration
	// MaxConnections and MaxConnectionsPerIP bound admitted TCP connections.
	// Zero selects the secure defaults; a negative value disables that limit.
	MaxConnections      int
	MaxConnectionsPerIP int
	Logf                func(format string, args ...any)
	Ready               func(net.Addr)

	sessions  *sessionRegistry
	slots     chan struct{} // counting semaphore; nil when unlimited
	admission *connectionAdmission

	kvMu     sync.Mutex
	kvStores map[string]runner.Store // app ID -> shared store

	busMu   sync.Mutex
	busById map[string]*runner.MemBus // app ID -> shared pub/sub bus
}

const (
	DefaultMaxConcurrentSessions = 64
	DefaultHandshakeTimeout      = 10 * time.Second
	DefaultIdleTimeout           = 5 * time.Minute
	DefaultMaxConnections        = 1024
	DefaultMaxConnectionsPerIP   = 32
)

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if s.Backend == nil {
		return errors.New("gateway: backend is required")
	}
	if s.Runner == nil {
		s.Runner = runner.New()
	}
	if s.sessions == nil {
		s.sessions = newSessionRegistry()
	}
	if source, ok := s.Backend.(SuspensionSource); ok {
		if err := source.StartSuspensionWatcher(ctx, s.handleSuspension); err != nil {
			return err
		}
	}
	if s.slots == nil && s.MaxConcurrentSessions > 0 {
		s.slots = make(chan struct{}, s.MaxConcurrentSessions)
	}
	if s.admission == nil {
		s.admission = newConnectionAdmission(
			effectiveLimit(s.MaxConnections, DefaultMaxConnections),
			effectiveLimit(s.MaxConnectionsPerIP, DefaultMaxConnectionsPerIP),
		)
	}
	// A key is optional. Public-key auth is attempted first by normal SSH
	// clients; clients without a usable key fall through to a prompt-free
	// keyboard-interactive method that represents an anonymous connection.
	// "none" cannot be enabled here: clients use it to discover auth methods, so
	// accepting it would make even key-bearing clients anonymous.
	cfg := optionalAuthConfig()
	signer := s.HostKey
	if signer == nil {
		var err error
		signer, err = devHostKey()
		if err != nil {
			return err
		}
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
		clientIP := connectionIP(conn.RemoteAddr())
		if !s.admission.acquire(clientIP) {
			s.logf("reject connection from %s: connection limit reached", conn.RemoteAddr())
			_ = conn.Close()
			continue
		}
		go func() {
			defer s.admission.release(clientIP)
			s.handleConn(ctx, conn, cfg)
		}()
	}
}

func optionalAuthConfig() *ssh.ServerConfig {
	return &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) {
			// Accept the candidate key, but attach no identity yet: this callback
			// is also invoked for unsigned public-key queries.
			return &ssh.Permissions{}, nil
		},
		VerifiedPublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey, permissions *ssh.Permissions, _ string) (*ssh.Permissions, error) {
			if permissions.Extensions == nil {
				permissions.Extensions = make(map[string]string)
			}
			// This callback only runs after crypto/ssh verifies the signature,
			// making the fingerprint a proved identity claim.
			permissions.Extensions["pubkey-fp"] = ssh.FingerprintSHA256(key)
			return permissions, nil
		},
		KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, _ ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			return &ssh.Permissions{Extensions: map[string]string{"auth-kind": "anonymous"}}, nil
		},
	}
}

func (s *Server) handleSuspension(ctx context.Context, event Suspension) error {
	n, err := s.sessions.killAndWait(ctx, event.Scope, event.ID)
	if err != nil {
		return err
	}
	s.logf("suspension acknowledged: scope=%d id=%q sessions=%d", event.Scope, event.ID, n)
	return nil
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func (s *Server) handleConn(ctx context.Context, nConn net.Conn, cfg *ssh.ServerConfig) {
	defer nConn.Close()
	conn := newActivityConn(nConn, effectiveDuration(s.IdleTimeout, DefaultIdleTimeout))
	if timeout := effectiveDuration(s.HandshakeTimeout, DefaultHandshakeTimeout); timeout > 0 {
		_ = nConn.SetDeadline(time.Now().Add(timeout))
	}
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		s.logf("ssh handshake from %s failed: %v", nConn.RemoteAddr(), err)
		return
	}
	conn.enableIdleDeadline()
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	identity := s.identityFromConn(sshConn)
	s.logf("connected: user=%q id=%q from %s", sshConn.User(), identity.User, nConn.RemoteAddr())

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
		go s.handleSession(ctx, ch, chReqs, sshConn.User(), identity)
	}
}

// identityFromConn distinguishes three cases: a registered, proved key is an
// authenticated identity; an unregistered, proved key is a stable but
// unauthenticated key identity; and a connection using no key is anonymous with
// an ephemeral per-connection id.
func (s *Server) identityFromConn(c *ssh.ServerConn) runner.Identity {
	if c.Permissions != nil {
		if fp := c.Permissions.Extensions["pubkey-fp"]; fp != "" {
			identity, err := s.Backend.ResolveIdentity(fp)
			if err == nil && identity.User != "" {
				return identity
			}
			if err != nil {
				s.logf("resolve SSH identity %q: %v", fp, err)
			}
			// Resolution failures fail closed. Possession of the key is proved,
			// but the gateway must not assert that it belongs to a platform owner.
			return runner.Identity{User: fp}
		}
	}
	sid := c.SessionID()
	if len(sid) > 8 {
		sid = sid[:8]
	}
	return runner.Identity{User: "anonymous:" + hex.EncodeToString(sid)}
}

func HostFromListen(listenHost string) string {
	switch listenHost {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return strings.Trim(listenHost, "[]")
	}
}
