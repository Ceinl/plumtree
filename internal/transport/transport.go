// Package transport contains the narrow discovery, identity, SSH policy, and
// HTTP/1.1 stream building blocks for the clean external transport.
package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/Ceinl/plumtree/internal/protocol/control"
	"github.com/Ceinl/plumtree/internal/protocol/exec"
	"github.com/Ceinl/plumtree/internal/protocol/pairing"
)

const (
	PairSubsystem    = pairing.ProtocolName
	ControlSubsystem = control.ProtocolName
)

var (
	ErrNoServer             = errors.New("transport: no Plumtree server found")
	ErrAmbiguous            = errors.New("transport: ambiguous Plumtree server")
	ErrWrongService         = errors.New("transport: endpoint is not a Plumtree service")
	ErrHostKeyChanged       = errors.New("transport: SSH host key changed")
	ErrServerIDChanged      = errors.New("transport: server identity changed")
	ErrVersionMismatch      = errors.New("transport: product version mismatch")
	ErrConfirmationRequired = errors.New("transport: explicit host-key confirmation required")
	ErrNotAuthorized        = errors.New("transport: operation not authorized")
	ErrLeafDenied           = errors.New("transport: leaf access denied")
	ErrSpoofedIdentity      = errors.New("transport: spoofed identity")
	ErrInvalidPrincipal     = errors.New("transport: invalid principal")
)

type Endpoint struct {
	Host string
	Port int
}

func (e Endpoint) String() string { return net.JoinHostPort(e.Host, strconv.Itoa(e.Port)) }

type ServerInfo struct {
	StableID           string
	HostKeyAlgorithm   string
	HostKeyFingerprint string
	ProductVersion     string
	Protocols          []string
}

func (s ServerInfo) Supports(protocol string) bool {
	for _, p := range s.Protocols {
		if p == protocol {
			return true
		}
	}
	return false
}

type Probe func(context.Context, Endpoint) (ServerInfo, error)

// Discover probes an explicit port only when supplied; otherwise 2222 is
// tried before the conventional SSH port 22. Non-Plumtree endpoints are
// skipped, while two distinct successful identities are ambiguous.
func Discover(ctx context.Context, host string, explicitPort int, probe Probe) (Endpoint, ServerInfo, error) {
	if strings.TrimSpace(host) == "" || strings.ContainsAny(host, "/\\") || probe == nil {
		return Endpoint{}, ServerInfo{}, fmt.Errorf("%w: invalid host or probe", ErrNoServer)
	}
	ports := []int{2222, 22}
	if explicitPort != 0 {
		ports = []int{explicitPort}
	}
	var found []struct {
		endpoint Endpoint
		info     ServerInfo
	}
	var lastErr error
	for _, port := range ports {
		if port < 1 || port > 65535 {
			continue
		}
		endpoint := Endpoint{Host: host, Port: port}
		info, err := probe(ctx, endpoint)
		if err != nil {
			lastErr = err
			continue
		}
		if info.StableID == "" || info.HostKeyFingerprint == "" ||
			!info.Supports(pairing.ProtocolName) || !info.Supports(control.ProtocolName) {
			lastErr = ErrWrongService
			continue
		}
		found = append(found, struct {
			endpoint Endpoint
			info     ServerInfo
		}{endpoint, info})
	}
	if len(found) == 0 {
		if lastErr != nil {
			return Endpoint{}, ServerInfo{}, fmt.Errorf("%w: %w", ErrNoServer, lastErr)
		}
		return Endpoint{}, ServerInfo{}, ErrNoServer
	}
	if len(found) > 1 && (found[0].info.StableID != found[1].info.StableID ||
		found[0].info.HostKeyFingerprint != found[1].info.HostKeyFingerprint) {
		return Endpoint{}, ServerInfo{}, ErrAmbiguous
	}
	return found[0].endpoint, found[0].info, nil
}

type HostPin struct {
	Endpoint       Endpoint
	StableID       string
	Algorithm      string
	Fingerprint    string
	ProductVersion string
}

func FirstUsePin(endpoint Endpoint, info ServerInfo, confirmed bool) (HostPin, error) {
	if !confirmed {
		return HostPin{}, ErrConfirmationRequired
	}
	if endpoint.Host == "" || endpoint.Port < 1 || endpoint.Port > 65535 ||
		info.StableID == "" || info.HostKeyAlgorithm == "" || info.HostKeyFingerprint == "" || info.ProductVersion == "" ||
		!info.Supports(pairing.ProtocolName) || !info.Supports(control.ProtocolName) {
		return HostPin{}, fmt.Errorf("%w: incomplete server identity", ErrNoServer)
	}
	return HostPin{Endpoint: endpoint, StableID: info.StableID, Algorithm: info.HostKeyAlgorithm,
		Fingerprint: info.HostKeyFingerprint, ProductVersion: info.ProductVersion}, nil
}

func (p HostPin) Verify(endpoint Endpoint, info ServerInfo) error {
	if p.Endpoint != endpoint || p.Fingerprint != info.HostKeyFingerprint || p.Algorithm != info.HostKeyAlgorithm {
		return ErrHostKeyChanged
	}
	if p.StableID != info.StableID {
		return ErrServerIDChanged
	}
	if p.ProductVersion != info.ProductVersion {
		return ErrVersionMismatch
	}
	return nil
}

func RequireProtocols(info ServerInfo, expectedProduct string) error {
	if err := control.ValidateVersion(info.ProductVersion, expectedProduct); err != nil {
		return fmt.Errorf("%w: %v", ErrVersionMismatch, err)
	}
	if !info.Supports(pairing.ProtocolName) || !info.Supports(control.ProtocolName) {
		return fmt.Errorf("%w: required SSH protocols missing", ErrWrongService)
	}
	return nil
}

type DevicePrincipal struct {
	ServerID    string `json:"serverID"`
	AuthorID    string `json:"authorID"`
	DeviceID    string `json:"deviceID"`
	Fingerprint string `json:"fingerprint"`
}

func (p DevicePrincipal) validate() error {
	if p.ServerID == "" || p.AuthorID == "" || p.DeviceID == "" || p.Fingerprint == "" {
		return ErrInvalidPrincipal
	}
	return nil
}

type PrincipalEnvelope struct {
	Protocol  string          `json:"protocol"`
	ServerID  string          `json:"serverID"`
	Principal DevicePrincipal `json:"principal"`
	Nonce     []byte          `json:"nonce"`
	Signature []byte          `json:"signature"`
}

func (e PrincipalEnvelope) signingBytes() ([]byte, error) {
	if e.Protocol != control.ProtocolName || e.ServerID == "" || e.ServerID != e.Principal.ServerID ||
		len(e.Nonce) < 16 || len(e.Nonce) > 64 || e.Principal.validate() != nil {
		return nil, ErrInvalidPrincipal
	}
	copy := e
	copy.Signature = nil
	return json.Marshal(copy)
}

func SignPrincipal(privateKey ed25519.PrivateKey, principal DevicePrincipal, nonce []byte) (PrincipalEnvelope, error) {
	if len(privateKey) != ed25519.PrivateKeySize || len(nonce) < 16 || len(nonce) > 64 || principal.validate() != nil {
		return PrincipalEnvelope{}, ErrInvalidPrincipal
	}
	e := PrincipalEnvelope{Protocol: control.ProtocolName, ServerID: principal.ServerID, Principal: principal, Nonce: append([]byte(nil), nonce...)}
	data, err := e.signingBytes()
	if err != nil {
		return PrincipalEnvelope{}, err
	}
	e.Signature = ed25519.Sign(privateKey, data)
	return e, nil
}

func VerifyPrincipal(publicKey ed25519.PublicKey, envelope PrincipalEnvelope) (DevicePrincipal, error) {
	if len(publicKey) != ed25519.PublicKeySize || len(envelope.Signature) != ed25519.SignatureSize {
		return DevicePrincipal{}, ErrInvalidPrincipal
	}
	data, err := envelope.signingBytes()
	if err != nil || !ed25519.Verify(publicKey, data, envelope.Signature) {
		return DevicePrincipal{}, ErrSpoofedIdentity
	}
	return envelope.Principal, nil
}

type PrincipalKind string

const (
	PrincipalCandidate PrincipalKind = "candidate"
	PrincipalDevice    PrincipalKind = "device"
	PrincipalVisitor   PrincipalKind = "visitor"
	PrincipalAnonymous PrincipalKind = "anonymous"
)

type Principal struct {
	Kind        PrincipalKind
	ServerID    string
	AuthorID    string
	DeviceID    string
	Fingerprint string
	Active      bool
}

type PairingMode string

const (
	PairNewAuthor PairingMode = "new-author"
	PairAddDevice PairingMode = "add-device"
	PairRecovery  PairingMode = "recovery"
)

func AuthorizeSubsystem(p Principal, subsystem string) error {
	switch subsystem {
	case PairSubsystem:
		if p.Kind != PrincipalCandidate {
			return ErrNotAuthorized
		}
	case ControlSubsystem:
		if p.Kind != PrincipalDevice || !p.Active {
			return ErrNotAuthorized
		}
	default:
		return ErrNotAuthorized
	}
	return nil
}

func AuthorizePairing(p Principal, mode PairingMode, allowNewAuthors, allowNewDevices bool) error {
	if p.Kind != PrincipalCandidate {
		return ErrNotAuthorized
	}
	switch mode {
	case PairNewAuthor:
		if !allowNewAuthors {
			return ErrNotAuthorized
		}
	case PairAddDevice, PairRecovery:
		if !allowNewDevices {
			return ErrNotAuthorized
		}
	default:
		return ErrNotAuthorized
	}
	return nil
}

func AuthorizeControl(p Principal, serverID string) error {
	if err := AuthorizeSubsystem(p, ControlSubsystem); err != nil {
		return err
	}
	if serverID == "" || p.ServerID != serverID {
		return ErrSpoofedIdentity
	}
	return nil
}

func AuthorizeForwarding(Principal) error { return ErrNotAuthorized }
func AuthorizeShell(Principal) error      { return ErrNotAuthorized }

func AuthorizeLeafExec(p Principal, command string) ([]string, error) {
	if p.Kind != PrincipalVisitor && p.Kind != PrincipalDevice {
		return nil, ErrNotAuthorized
	}
	if strings.TrimSpace(command) == "" {
		return nil, ErrNotAuthorized
	}
	parsed, err := execprotocol.ParseExecCommand(command)
	if err != nil {
		return nil, err
	}
	return parsed, nil
}

type LeafAccess string

const (
	LeafPublic     LeafAccess = "public"
	LeafRestricted LeafAccess = "restricted"
)

type LeafPrincipal struct {
	Fingerprint   string
	Authenticated bool
}

func AuthorizeLeaf(access LeafAccess, fingerprint string, keyProved bool, allowlist map[string]bool) (LeafPrincipal, error) {
	switch access {
	case LeafPublic:
		if !keyProved {
			return LeafPrincipal{}, nil
		}
		if fingerprint == "" {
			return LeafPrincipal{}, ErrLeafDenied
		}
		return LeafPrincipal{Fingerprint: fingerprint, Authenticated: true}, nil
	case LeafRestricted:
		if !keyProved || fingerprint == "" || !allowlist[fingerprint] {
			return LeafPrincipal{}, ErrLeafDenied
		}
		return LeafPrincipal{Fingerprint: fingerprint, Authenticated: true}, nil
	default:
		return LeafPrincipal{}, ErrLeafDenied
	}
}

type RoleInfo struct {
	Name           string
	ProductVersion string
	Protocols      []string
}

func CheckRoleParity(a, b RoleInfo, expectedProduct string) error {
	if err := control.ValidateVersion(a.ProductVersion, expectedProduct); err != nil {
		return err
	}
	if err := control.ValidateVersion(b.ProductVersion, expectedProduct); err != nil {
		return err
	}
	for _, protocol := range []string{pairing.ProtocolName, control.ProtocolName} {
		if !contains(a.Protocols, protocol) || !contains(b.Protocols, protocol) {
			return fmt.Errorf("%w: %s missing", ErrWrongService, protocol)
		}
	}
	return nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func RejectIdentityHeaders(headers http.Header) error {
	for name, values := range headers {
		if control.IsIdentityHeader(name) && len(values) > 0 && values[0] != "" {
			return ErrSpoofedIdentity
		}
	}
	return nil
}

func FingerprintPublicKey(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// EqualFingerprint avoids accidentally accepting a user-controlled value via
// a non-constant-time comparison in callers that handle sensitive principals.
func EqualFingerprint(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
