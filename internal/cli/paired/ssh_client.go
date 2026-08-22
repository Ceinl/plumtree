package paired

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
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
	"github.com/Ceinl/plumtree/internal/protocol/pairing"
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
	if cfg.Timeout > 0 {
		_ = raw.SetDeadline(time.Now().Add(cfg.Timeout))
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
	_ = raw.SetDeadline(time.Time{})
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
	Info      transport.ServerInfo
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
	hello, err := ReadServerHello(ctx, stream)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	info := serverInfoFromHello(hello)
	if err := transport.RequireProtocols(info, record.ProductVersion); err != nil || info.StableID != record.ServerID || info.HostKeyAlgorithm != record.HostKeyAlgorithm || info.HostKeyFingerprint != record.HostKeyFingerprint {
		_ = stream.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: pairing server identity changed", transport.ErrServerIDChanged)
	}
	return &PairingConnection{Channel: stream, SSH: client, SessionID: base64.RawStdEncoding.EncodeToString(client.Conn.SessionID()), Info: info, stream: stream}, nil
}

// LiveExchange connects the manager's confirmed pin and generated device key
// to the bounded pairing subsystem.
func LiveExchange(ctx context.Context, pin transport.HostPin, signer ssh.Signer, input PairInput) (PairResult, error) {
	record := ServerRecord{Name: "pairing", ServerID: pin.StableID, Host: pin.Endpoint.Host, Port: pin.Endpoint.Port,
		HostKeyAlgorithm: pin.Algorithm, HostKeyFingerprint: pin.Fingerprint, ProductVersion: pin.ProductVersion, KeyRef: "unused"}
	connection, err := DialPairing(ctx, record, DialConfig{Signer: signer, Timeout: 15 * time.Second})
	if err != nil {
		return PairResult{}, err
	}
	defer connection.Close()
	transcript, err := NewTranscript(connection.SessionID, pin, signer, input.Purpose, input.Identifier)
	if err != nil {
		return PairResult{}, err
	}
	return ExchangePairing(ctx, connection.Channel, transcript, input.Secret, ExchangeOptions{DeviceName: input.DeviceName, RecoverySecret: input.RecoverySecret})
}

// ProbeEndpoint proves that an endpoint is a Plumtree SSH listener by opening
// only the pairing subsystem and comparing its greeting with the negotiated
// host key. The temporary probe key is never persisted or enrolled.
func ProbeEndpoint(ctx context.Context, endpoint transport.Endpoint, timeout time.Duration) (transport.ServerInfo, error) {
	_, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		return transport.ServerInfo{}, err
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		return transport.ServerInfo{}, err
	}
	var observedAlgorithm, observedFingerprint string
	config := &ssh.ClientConfig{User: "plumtree", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, Timeout: timeout,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			observedAlgorithm, observedFingerprint = key.Type(), ssh.FingerprintSHA256(key)
			return nil
		}}
	dialer := net.Dialer{Timeout: timeout}
	raw, err := dialer.DialContext(ctx, "tcp", endpoint.String())
	if err != nil {
		return transport.ServerInfo{}, err
	}
	if timeout > 0 {
		_ = raw.SetDeadline(time.Now().Add(timeout))
	}
	conn, channels, requests, err := ssh.NewClientConn(raw, endpoint.String(), config)
	if err != nil {
		_ = raw.Close()
		return transport.ServerInfo{}, fmt.Errorf("%w: probe handshake: %v", ErrSSHTransport, err)
	}
	client := ssh.NewClient(conn, channels, requests)
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return transport.ServerInfo{}, fmt.Errorf("%w: probe session: %v", ErrSSHTransport, err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return transport.ServerInfo{}, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return transport.ServerInfo{}, err
	}
	if err := session.RequestSubsystem(transport.PairSubsystem); err != nil {
		_ = session.Close()
		_ = client.Close()
		return transport.ServerInfo{}, fmt.Errorf("%w: pairing subsystem: %v", transport.ErrWrongService, err)
	}
	stream := &sessionStream{in: stdin, out: stdout, sess: session, client: client}
	hello, err := ReadServerHello(ctx, stream)
	_ = stream.Close()
	if err != nil {
		return transport.ServerInfo{}, err
	}
	info := serverInfoFromHello(hello)
	if info.HostKeyAlgorithm != observedAlgorithm || info.HostKeyFingerprint != observedFingerprint {
		return transport.ServerInfo{}, transport.ErrHostKeyChanged
	}
	if !info.Supports(transport.PairSubsystem) || !info.Supports(transport.ControlSubsystem) {
		return transport.ServerInfo{}, transport.ErrWrongService
	}
	return info, nil
}

func NewProbe(timeout time.Duration) transport.Probe {
	return func(ctx context.Context, endpoint transport.Endpoint) (transport.ServerInfo, error) {
		return ProbeEndpoint(ctx, endpoint, timeout)
	}
}

func serverInfoFromHello(hello pairing.ServerHello) transport.ServerInfo {
	return transport.ServerInfo{StableID: hello.ServerID, HostKeyAlgorithm: hello.HostKeyAlgorithm, HostKeyFingerprint: hello.HostKeyFingerprint,
		ProductVersion: hello.ProductVersion, Protocols: append([]string(nil), hello.Protocols...)}
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
	Product    string           `json:"product"`
	Version    string           `json:"version"`
	APIVersion int              `json:"apiVersion"`
	ABIVersion int              `json:"abiVersion"`
	Limits     map[string]int64 `json:"limits"`
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
