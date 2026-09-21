// Package pairing defines the bounded, transcript-bound plumtree-pair-v1
// protocol contract. It carries proofs only; private keys and phrases never
// appear in the wire structures.
package pairing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	ProtocolName  = "plumtree-pair-v1"
	MaxFrameSize  = 64 << 10
	MaxSecretSize = 4096

	FrameServerHello byte = 1
	FrameClientHello byte = 2
	FrameServerProof byte = 3
	FrameClientProof byte = 4
	FrameComplete    byte = 5
	FrameProblem     byte = 6
)

var (
	ErrInvalid       = errors.New("pairing: invalid message")
	ErrProofMismatch = errors.New("pairing: proof mismatch")
	ErrFrameTooLarge = errors.New("pairing: frame too large")
)

type Purpose string

const (
	PurposeNewAuthor        Purpose = "new-author"
	PurposeAddDevice        Purpose = "add-device"
	PurposeOfflineRecovery  Purpose = "offline-recovery"
	PurposeOperatorRecovery Purpose = "operator-recovery"
)

// Transcript binds both SSH endpoints, both fresh nonces, the proposed key,
// and the purpose. JSON field order is fixed by the struct for stable MACs.
type Transcript struct {
	SessionID          string  `json:"sessionID"`
	ServerID           string  `json:"serverID"`
	HostKeyFingerprint string  `json:"hostKeyFingerprint"`
	ClientNonce        []byte  `json:"clientNonce"`
	ServerNonce        []byte  `json:"serverNonce"`
	PublicKey          string  `json:"publicKey"`
	DeviceFingerprint  string  `json:"deviceFingerprint"`
	Purpose            Purpose `json:"purpose"`
	Identifier         string  `json:"identifier"`
}

type ServerHello struct {
	ServerID           string   `json:"serverID"`
	HostKeyAlgorithm   string   `json:"hostKeyAlgorithm"`
	HostKeyFingerprint string   `json:"hostKeyFingerprint"`
	ProductVersion     string   `json:"productVersion"`
	Protocols          []string `json:"protocols"`
}

type ClientHello struct {
	Transcript       Transcript `json:"transcript"`
	DeviceName       string     `json:"deviceName,omitempty"`
	RecoverySalt     []byte     `json:"recoverySalt,omitempty"`
	RecoveryVerifier []byte     `json:"recoveryVerifier,omitempty"`
}

type Proof struct {
	Salt        []byte `json:"salt,omitempty"`
	ServerNonce []byte `json:"serverNonce,omitempty"`
	MAC         []byte `json:"mac"`
}

type Complete struct {
	ServerID     string `json:"serverID"`
	AuthorID     string `json:"authorID"`
	AuthorHandle string `json:"authorHandle"`
	DeviceID     string `json:"deviceID"`
}

type Problem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (t Transcript) Validate() error {
	for name, value := range map[string]string{
		"sessionID": t.SessionID, "serverID": t.ServerID,
		"hostKeyFingerprint": t.HostKeyFingerprint, "publicKey": t.PublicKey,
		"deviceFingerprint": t.DeviceFingerprint, "identifier": t.Identifier,
	} {
		if value == "" || len(value) > 4096 {
			return fmt.Errorf("%w: %s", ErrInvalid, name)
		}
	}
	if len(t.ClientNonce) < 16 || len(t.ClientNonce) > 64 || len(t.ServerNonce) < 16 || len(t.ServerNonce) > 64 {
		return fmt.Errorf("%w: nonce", ErrInvalid)
	}
	switch t.Purpose {
	case PurposeNewAuthor, PurposeAddDevice, PurposeOfflineRecovery, PurposeOperatorRecovery:
	default:
		return fmt.Errorf("%w: purpose", ErrInvalid)
	}
	return nil
}

func (t Transcript) canonical() ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(t)
}

// DeriveKey binds a high-entropy out-of-band phrase to this protocol label.
func DeriveKey(secret []byte) ([]byte, error) {
	if len(secret) < 16 || len(secret) > MaxSecretSize {
		return nil, fmt.Errorf("%w: secret length", ErrInvalid)
	}
	h := sha256.New()
	_, _ = h.Write([]byte(ProtocolName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(secret)
	return h.Sum(nil), nil
}

// DeriveVerifier makes a caller-held phrase usable for transcript proofs
// without placing the phrase itself in SQLite or on the wire.
func DeriveVerifier(salt, secret []byte) ([]byte, error) {
	if len(salt) == 0 || len(salt) > 1024 || len(secret) < 16 || len(secret) > MaxSecretSize {
		return nil, fmt.Errorf("%w: verifier material", ErrInvalid)
	}
	h := sha256.New()
	_, _ = h.Write(salt)
	_, _ = h.Write(secret)
	return h.Sum(nil), nil
}

func proof(secret []byte, t Transcript, label string) ([]byte, error) {
	key, err := DeriveKey(secret)
	if err != nil {
		return nil, err
	}
	transcript, err := t.canonical()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(ProtocolName))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(label))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(transcript)
	return mac.Sum(nil), nil
}

func ServerProof(secret []byte, t Transcript) ([]byte, error) { return proof(secret, t, "server") }
func ClientProof(secret []byte, t Transcript) ([]byte, error) { return proof(secret, t, "client") }

func VerifyServerProof(secret []byte, t Transcript, received []byte) error {
	expected, err := ServerProof(secret, t)
	if err != nil {
		return err
	}
	if !hmac.Equal(expected, received) {
		return ErrProofMismatch
	}
	return nil
}

func VerifyClientProof(secret []byte, t Transcript, received []byte) error {
	expected, err := ClientProof(secret, t)
	if err != nil {
		return err
	}
	if !hmac.Equal(expected, received) {
		return ErrProofMismatch
	}
	return nil
}

type Frame struct {
	Type    byte
	Payload []byte
}

// WriteFrame uses a four-byte big-endian length including the one-byte type.
func WriteFrame(w io.Writer, frame Frame) error {
	if len(frame.Payload)+1 > MaxFrameSize {
		return ErrFrameTooLarge
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(frame.Payload)+1))
	if err := writeFull(w, header[:]); err != nil {
		return err
	}
	if err := writeFull(w, []byte{frame.Type}); err != nil {
		return err
	}
	return writeFull(w, frame.Payload)
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func ReadFrame(r io.Reader) (Frame, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size < 1 || size > MaxFrameSize {
		return Frame{}, ErrFrameTooLarge
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return Frame{}, err
	}
	return Frame{Type: payload[0], Payload: append([]byte(nil), payload[1:]...)}, nil
}
