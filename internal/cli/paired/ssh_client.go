package paired

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/internal/protocol/control"
	"github.com/Ceinl/plumtree/internal/transport"
	"golang.org/x/crypto/ssh"
)

var (
	ErrSSHTransport = errors.New("paired: SSH transport failed")
	ErrHostKey      = errors.New("paired: host key pin rejected")
	ErrPreflight    = errors.New("paired: version preflight failed")
)

type DialConfig struct {
	Signer        ssh.Signer
	KeyStore      KeyStore
	User          string
	Timeout       time.Duration
	ClientVersion string
}

// ControlConnection owns the SSH client, subsystem session, and HTTP stream.
// Callers must close it; Close is idempotent.
type ControlConnection struct {
	HTTP  *http.Client
	SSH   *ssh.Client
	close func() error
	mu    sync.Once
	err   error
}

func (c *ControlConnection) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Do(func() {
		if c.close != nil {
			c.err = c.close()
		}
	})
	return c.err
}

type sessionStream struct {
	in     io.WriteCloser
	out    io.Reader
	sess   *ssh.Session
	client *ssh.Client
	once   sync.Once
}

func (s *sessionStream) Read(p []byte) (int, error)  { return s.out.Read(p) }
func (s *sessionStream) Write(p []byte) (int, error) { return s.in.Write(p) }
func (s *sessionStream) Close() error {
	var err error
	s.once.Do(func() {
		if e := s.sess.Close(); e != nil && !errors.Is(e, io.EOF) {
			err = e
		}
		if e := s.client.Close(); e != nil && err == nil {
			err = e
		}
	})
	return err
}

func (c *ControlConnection) setClose(closer io.Closer, session *ssh.Session, client *ssh.Client, cancel context.CancelFunc) {
	c.close = func() error {
		cancel()
		var first error
		if err := closer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			first = err
		}
		if err := session.Close(); err != nil && first == nil && !errors.Is(err, io.EOF) {
			first = err
		}
		if err := client.Close(); err != nil && first == nil && !errors.Is(err, net.ErrClosed) {
			first = err
		}
		return first
	}
}

// DialControl authenticates with the per-server Ed25519 device key, verifies
// the pinned host key, opens only plumtree-control-v1, and returns an HTTP/1.1
// client over that subsystem. No external HTTP listener or bearer credential
// is involved.
func DialControl(ctx context.Context, record ServerRecord, cfg DialConfig) (*ControlConnection, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := record.validate(); err != nil {
		return nil, err
	}
	client, session, stream, err := dialSubsystem(ctx, record, cfg, transport.ControlSubsystem)
	if err != nil {
		return nil, err
	}
	product := record.ProductVersion
	if cfg.ClientVersion != "" {
		product = cfg.ClientVersion
	}
	httpClient, closer, err := transport.NewHTTPClient(stream, product)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	controlCtx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Close()
		case <-controlCtx.Done():
		}
	}()
	connection := &ControlConnection{HTTP: httpClient, SSH: client}
	connection.setClose(closer, session, client, cancel)
	return connection, nil
}

func dialSubsystem(ctx context.Context, record ServerRecord, cfg DialConfig, subsystem string) (*ssh.Client, *ssh.Session, *sessionStream, error) {
	signer := cfg.Signer
	if signer == nil && cfg.KeyStore != nil {
		var err error
		signer, err = cfg.KeyStore.Load(record.KeyRef)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w: device key: %v", ErrSSHTransport, err)
		}
	}
	if signer == nil {
		return nil, nil, nil, fmt.Errorf("%w: device signer is required", ErrSSHTransport)
	}
	user := cfg.User
	if user == "" {
		user = "plumtree"
	}
	sshConfig := &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: pinnedHostKey(record), Timeout: cfg.Timeout}
	address := record.Endpoint().String()
	dialer := &net.Dialer{Timeout: cfg.Timeout}
	raw, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return nil, nil, nil, fmt.Errorf("%w: dial: %v", ErrSSHTransport, err)
	}
	conn, chans, requests, err := ssh.NewClientConn(raw, address, sshConfig)
	if err != nil {
		_ = raw.Close()
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		if errors.Is(err, ssh.ErrNoAuth) {
			return nil, nil, nil, fmt.Errorf("%w: authentication: %v", ErrSSHTransport, err)
		}
		return nil, nil, nil, fmt.Errorf("%w: handshake: %v", ErrSSHTransport, err)
	}
	client := ssh.NewClient(conn, chans, requests)
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, nil, nil, fmt.Errorf("%w: session: %v", ErrSSHTransport, err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, nil, nil, fmt.Errorf("%w: stdin: %v", ErrSSHTransport, err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, nil, nil, fmt.Errorf("%w: stdout: %v", ErrSSHTransport, err)
	}
	if err := session.RequestSubsystem(subsystem); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, nil, nil, fmt.Errorf("%w: %s subsystem: %v", ErrSSHTransport, subsystem, err)
	}
	return client, session, &sessionStream{in: stdin, out: stdout, sess: session, client: client}, nil
}

// PairingConnection owns an authenticated plumtree-pair-v1 subsystem. The
// session ID is exposed so callers can bind it into pairing.Transcript.
type PairingConnection struct {
	Channel   io.ReadWriteCloser
	SSH       *ssh.Client
	SessionID string
	stream    *sessionStream
	mu        sync.Once
}

func (c *PairingConnection) Close() error {
	if c == nil || c.stream == nil {
		return nil
	}
	var err error
	c.mu.Do(func() { err = c.stream.Close() })
	return err
}

func DialPairing(ctx context.Context, record ServerRecord, cfg DialConfig) (*PairingConnection, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := record.validate(); err != nil {
		return nil, err
	}
	client, _, stream, err := dialSubsystem(ctx, record, cfg, transport.PairSubsystem)
	if err != nil {
		return nil, err
	}
	return &PairingConnection{Channel: stream, SSH: client, SessionID: string(client.Conn.SessionID()), stream: stream}, nil
}

func pinnedHostKey(record ServerRecord) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if key == nil || key.Type() != record.HostKeyAlgorithm ||
			!equalString(ssh.FingerprintSHA256(key), record.HostKeyFingerprint) {
			return fmt.Errorf("%w: %w", ErrHostKey, transport.ErrHostKeyChanged)
		}
		if record.HostKeyPublicKey != "" {
			expected, _, _, _, err := ssh.ParseAuthorizedKey([]byte(record.HostKeyPublicKey))
			if err != nil || expected == nil || !bytes.Equal(bytes.TrimSpace(ssh.MarshalAuthorizedKey(expected)), bytes.TrimSpace(ssh.MarshalAuthorizedKey(key))) {
				return fmt.Errorf("%w: public key mismatch", ErrHostKey)
			}
		}
		return nil
	}
}

func equalString(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

type VersionInfo struct {
	Product    string `json:"product"`
	Version    string `json:"version"`
	APIVersion int    `json:"apiVersion"`
	ABIVersion int    `json:"abiVersion"`
}

// Preflight calls the version endpoint before any state-changing operation.
// It requires an exact product-version match and bounds the response body.
func Preflight(ctx context.Context, client *http.Client, expectedProduct string, expectedABI int) (VersionInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil || strings.TrimSpace(expectedProduct) == "" {
		return VersionInfo{}, fmt.Errorf("%w: invalid arguments", ErrPreflight)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://plumtree/api/v1/version", nil)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("%w: request: %v", ErrPreflight, err)
	}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return VersionInfo{}, ctx.Err()
		}
		return VersionInfo{}, fmt.Errorf("%w: %v", ErrPreflight, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return VersionInfo{}, fmt.Errorf("%w: server returned %s", ErrPreflight, response.Status)
	}
	var version VersionInfo
	dec := json.NewDecoder(io.LimitReader(response.Body, control.MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&version); err != nil {
		return VersionInfo{}, fmt.Errorf("%w: invalid response: %v", ErrPreflight, err)
	}
	actual := version.Version
	if actual == "" {
		actual = version.Product
	}
	if actual != expectedProduct {
		return VersionInfo{}, fmt.Errorf("%w: want %q got %q", transport.ErrVersionMismatch, expectedProduct, actual)
	}
	if expectedABI > 0 && version.ABIVersion != expectedABI {
		return VersionInfo{}, fmt.Errorf("%w: ABI want %d got %d", ErrPreflight, expectedABI, version.ABIVersion)
	}
	return version, nil
}
