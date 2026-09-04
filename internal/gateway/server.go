// Package gateway serves deployed Plumtree apps over SSH. It owns the SSH front
// end, the PTY/session lifecycle, and the per-session WASM sandbox, delegating
// all authoritative platform state to a Backend. It runs embedded in the
// control plane with an in-process Backend.
package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/internal/runner"
	"github.com/Ceinl/plumtree/internal/sshconn"
	"golang.org/x/crypto/ssh"
)

// Config is the static construction-time configuration for a gateway Server.
// It mirrors the previous exported Server fields so existing call sites read
// naturally; New validates the combinations the type alone cannot express and
// resolves zero/negative sentinels to their effective values.
type Config struct {
	// Backend is the port to the control plane (required).
	Backend Backend
	// Suspensions streams administrative suspension events to the kill switch
	// (required). It is explicit so a backend cannot compile while missing
	// kill-switch behavior.
	Suspensions SuspensionSource
	// Runner reuses compiled WASM across sessions. When nil, New creates one.
	Runner *runner.Runner
	Limits runner.Limits
	MaxFPS int
	// HostKey signs the SSH host key. When nil, a persistent dev host key under
	// the OS config dir is loaded or generated.
	HostKey ssh.Signer
	// RunnerWorker, when set, is the path to the runner-worker binary; all app
	// sessions then run the WASM sandbox in a separate local process. This is a
	// development fallback, not a native-RCE boundary: the process still shares
	// the gateway's container and OS identity.
	RunnerWorker string
	// RunnerEndpoint and RunnerToken select the production runner broker
	// (unix:///path/to/socket, tls://host:port, or tcp://host:port — the plain
	// TCP form ships traffic unencrypted and is refused in production). The
	// broker lives in a separate networkless container and owns the disposable
	// worker process, while this gateway retains credentials and capabilities.
	RunnerEndpoint string
	RunnerToken    string
	// EnableHostCommands gives claimed apps the ability to execute allowlisted
	// programs as the gateway OS user. It is off by default and intended only
	// for trusted apps on private/self-hosted servers. New fails closed
	// when it is enabled without a non-empty HostCommandAllowlist.
	EnableHostCommands bool
	// HostCommandAllowlist is the operator's executable allowlist consulted by
	// every host command; shells are always refused regardless of its contents.
	HostCommandAllowlist []string
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
}

// Server serves deployed apps over SSH. Construct it with New so every
// instance starts in one valid state: configuration is validated and resolved
// once, and the session registry, capacity slots, connection admission, and
// bus state are initialized before any session is handled.
type Server struct {
	backend     Backend
	suspensions SuspensionSource
	runner      *runner.Runner
	limits      runner.Limits
	maxFPS      int
	hostKey     ssh.Signer

	runnerWorker   string
	runnerEndpoint string
	runnerToken    string

	enableHostCommands   bool
	hostCommandAllowlist []string

	maxConcurrentSessions int

	// handshakeTimeout and idleTimeout hold the resolved deadlines: zero
	// selected the secure default in New, a negative value disabled the
	// deadline (resolved to 0, meaning no deadline), otherwise the configured
	// value.
	handshakeTimeout time.Duration
	idleTimeout      time.Duration

	logger func(format string, args ...any)
	ready  func(net.Addr)

	sessions  *sessionRegistry
	slots     chan struct{} // counting semaphore; nil when unlimited
	admission *sshconn.Admission

	busMu   sync.Mutex
	busById map[string]*runner.MemBus // app ID -> shared pub/sub bus

	startOnce sync.Once
	startErr  error
}

// New validates static configuration, applies defaults, and initializes the
// runner, session registry, capacity slots, connection admission, and bus
// state, so the returned Server is ready for concurrent session handling.
// Only context-bound lifecycle work (registering the suspension watcher)
// remains for Start.
func New(c Config) (*Server, error) {
	if c.Backend == nil {
		return nil, fmt.Errorf("%w: backend is required", ErrNotConfigured)
	}
	if c.Suspensions == nil {
		return nil, fmt.Errorf("%w: suspension source is required", ErrNotConfigured)
	}
	if c.RunnerEndpoint != "" && c.RunnerWorker != "" {
		return nil, errors.New("gateway: configure either runner endpoint or local runner worker, not both")
	}
	if c.RunnerEndpoint != "" && c.RunnerToken == "" {
		return nil, errors.New("gateway: runner token is required with runner endpoint")
	}
	if c.EnableHostCommands && len(c.HostCommandAllowlist) == 0 {
		return nil, errors.New("gateway: host commands enabled with an empty allowlist; refusing to start")
	}
	eng := c.Runner
	if eng == nil {
		eng = runner.New()
	}
	lim := c.Limits
	if lim.MemoryPages == 0 {
		lim = runner.DefaultLimits
	}
	var slots chan struct{}
	if c.MaxConcurrentSessions > 0 {
		slots = make(chan struct{}, c.MaxConcurrentSessions)
	}
	return &Server{
		backend:               c.Backend,
		suspensions:           c.Suspensions,
		runner:                eng,
		limits:                lim,
		maxFPS:                c.MaxFPS,
		hostKey:               c.HostKey,
		runnerWorker:          c.RunnerWorker,
		runnerEndpoint:        c.RunnerEndpoint,
		runnerToken:           c.RunnerToken,
		enableHostCommands:    c.EnableHostCommands,
		hostCommandAllowlist:  append([]string(nil), c.HostCommandAllowlist...),
		maxConcurrentSessions: c.MaxConcurrentSessions,
		handshakeTimeout:      effectiveDuration(c.HandshakeTimeout, DefaultHandshakeTimeout),
		idleTimeout:           effectiveDuration(c.IdleTimeout, DefaultIdleTimeout),
		logger:                c.Logf,
		ready:                 c.Ready,
		sessions:              newSessionRegistry(),
		slots:                 slots,
		admission: sshconn.NewAdmission(
			effectiveLimit(c.MaxConnections, DefaultMaxConnections),
			effectiveLimit(c.MaxConnectionsPerIP, DefaultMaxConnectionsPerIP),
		),
		busById: make(map[string]*runner.MemBus),
	}, nil
}

// HandleSession runs one already-authenticated SSH session channel. The root
// server uses this seam to multiplex leaf shell/exec with its private control
// and pairing subsystems on one public SSH listener. The Server must be
// constructed with New; HandleSession performs no initialization and is safe
// for concurrent use.
func (s *Server) HandleSession(ctx context.Context, ch ssh.Channel, requests <-chan *ssh.Request, handle string, identity runner.Identity) {
	s.handleSession(ctx, ch, requests, handle, identity)
}

const (
	DefaultHandshakeTimeout    = 10 * time.Second
	DefaultIdleTimeout         = 5 * time.Minute
	DefaultMaxConnections      = 1024
	DefaultMaxConnectionsPerIP = 32
)

func effectiveLimit(configured, fallback int) int {
	if configured == 0 {
		return fallback
	}
	if configured < 0 {
		return 0
	}
	return configured
}

func effectiveDuration(configured, fallback time.Duration) time.Duration {
	if configured == 0 {
		return fallback
	}
	if configured < 0 {
		return 0
	}
	return configured
}

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if err := s.Start(ctx); err != nil {
		return err
	}

	// A key is optional. Public-key auth is attempted first by normal SSH
	// clients; clients without a usable key fall through to a prompt-free
	// keyboard-interactive method that represents an anonymous connection.
	// "none" cannot be enabled here: clients use it to discover auth methods, so
	// accepting it would make even key-bearing clients anonymous.
	cfg := optionalAuthConfig()
	signer := s.hostKey
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
	if s.ready != nil {
		s.ready(ln.Addr())
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
		clientIP := sshconn.ClientIP(conn.RemoteAddr())
		if !s.admission.Acquire(clientIP) {
			s.logf("reject connection from %s: connection limit reached", conn.RemoteAddr())
			_ = conn.Close()
			continue
		}
		go func() {
			defer s.admission.Release(clientIP)
			s.handleConn(ctx, conn, cfg)
		}()
	}
}

// Start registers the suspension kill-switch before a listener admits leaf
// sessions. All static validation and initialization already happened in New;
// Start performs only this context-bound lifecycle work. It is safe to call
// more than once.
func (s *Server) Start(ctx context.Context) error {
	s.startOnce.Do(func() {
		s.startErr = s.suspensions.StartSuspensionWatcher(ctx, s.handleSuspension)
	})
	return s.startErr
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
	if s.logger != nil {
		s.logger(format, args...)
	}
}

func (s *Server) handleConn(ctx context.Context, nConn net.Conn, cfg *ssh.ServerConfig) {
	defer nConn.Close()
	conn := sshconn.NewActivityConn(nConn, s.idleTimeout)
	if timeout := s.handshakeTimeout; timeout > 0 {
		_ = nConn.SetDeadline(time.Now().Add(timeout))
	}
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		s.logf("ssh handshake from %s failed: %v", nConn.RemoteAddr(), err)
		return
	}
	conn.EnableIdleDeadline()
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	identity := s.identityFromConn(ctx, sshConn)
	s.logf("connection open app=%q identity=%q auth=%s from=%s", sshConn.User(), identityLogValue(identity), identity.Kind, nConn.RemoteAddr())

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

func identityLogValue(identity runner.Identity) string {
	value := identity.User
	if strings.HasPrefix(value, "SHA256:") && len(value) > 18 {
		return value[:18] + "…"
	}
	if strings.HasPrefix(value, "anonymous:") && len(value) > 18 {
		return value[:18] + "…"
	}
	return value
}

// identityFromConn distinguishes three cases: a registered, proved key is an
// authenticated identity; an unregistered, proved key is a stable but
// unauthenticated key identity; and a connection using no key is anonymous with
// an ephemeral per-connection id.
func (s *Server) identityFromConn(ctx context.Context, c *ssh.ServerConn) runner.Identity {
	if c.Permissions != nil {
		if fp := c.Permissions.Extensions["pubkey-fp"]; fp != "" {
			identity, err := s.backend.ResolveIdentity(ctx, fp)
			if err == nil && identity.User != "" {
				// Defense in depth: owner metadata on an unauthenticated
				// identity carries no authority and must never reach the
				// app-relative derivation.
				if !identity.Authenticated {
					identity.OwnerID = ""
				}
				return identity
			}
			if err != nil {
				s.logf("resolve SSH identity %q: %v", identityLogValue(runner.Identity{User: fp}), err)
			}
			// Resolution failures fail closed. Possession of the key is proved,
			// but the gateway must not assert that it belongs to a platform owner.
			return runner.Identity{User: fp, Kind: runner.IdentitySSHKey}
		}
	}
	sid := c.SessionID()
	if len(sid) > 8 {
		sid = sid[:8]
	}
	return runner.Identity{User: "anonymous:" + hex.EncodeToString(sid), Kind: runner.IdentityAnonymous}
}

func HostFromListen(listenHost string) string {
	switch listenHost {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return strings.Trim(listenHost, "[]")
	}
}
