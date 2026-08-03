package paired

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Ceinl/plumtree/internal/protocol/pairing"
	"github.com/Ceinl/plumtree/internal/transport"
	"golang.org/x/crypto/ssh"
)

const (
	pairHelloFrame    = 1
	pairProofFrame    = 2
	pairReplyFrame    = 3
	pairCompleteFrame = 4
)

var (
	ErrPairing      = errors.New("paired: pairing failed")
	ErrPairingFrame = errors.New("paired: invalid pairing frame")
)

type pairingHello struct {
	Transcript pairing.Transcript `json:"transcript"`
}

type pairingProof struct {
	ServerNonce []byte `json:"serverNonce,omitempty"`
	Proof       []byte `json:"proof"`
}

type PairResult struct {
	ServerID     string `json:"serverID"`
	AuthorID     string `json:"authorID"`
	AuthorHandle string `json:"authorHandle,omitempty"`
	DeviceID     string `json:"deviceID"`
}

// ExchangePairing runs the proof-only exchange on an already-authenticated
// plumtree-pair-v1 channel. The phrase is used only to derive MAC keys and is
// never serialized, persisted, or included in errors.
func ExchangePairing(ctx context.Context, channel io.ReadWriteCloser, transcript pairing.Transcript, secret []byte) (PairResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if channel == nil {
		return PairResult{}, fmt.Errorf("%w: nil channel", ErrPairing)
	}
	if err := transcript.Validate(); err != nil {
		return PairResult{}, fmt.Errorf("%w: %v", ErrPairing, err)
	}
	hello, err := json.Marshal(pairingHello{Transcript: transcript})
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: hello: %v", ErrPairing, err)
	}
	if err := pairing.WriteFrame(channel, pairing.Frame{Type: pairHelloFrame, Payload: hello}); err != nil {
		return PairResult{}, fmt.Errorf("%w: send hello: %v", ErrPairing, err)
	}
	frame, err := readPairFrame(ctx, channel)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: receive proof: %v", ErrPairing, err)
	}
	if frame.Type != pairProofFrame {
		return PairResult{}, fmt.Errorf("%w: expected server proof", ErrPairingFrame)
	}
	var proof pairingProof
	if err := decodePairPayload(frame.Payload, &proof); err != nil {
		return PairResult{}, err
	}
	if len(proof.ServerNonce) < 16 || len(proof.ServerNonce) > 64 {
		return PairResult{}, fmt.Errorf("%w: missing server nonce", ErrPairingFrame)
	}
	transcript.ServerNonce = append([]byte(nil), proof.ServerNonce...)
	if err := transcript.Validate(); err != nil {
		return PairResult{}, fmt.Errorf("%w: server transcript: %v", ErrPairing, err)
	}
	if err := pairing.VerifyServerProof(secret, transcript, proof.Proof); err != nil {
		return PairResult{}, fmt.Errorf("%w: server proof: %v", ErrPairing, err)
	}
	clientProof, err := pairing.ClientProof(secret, transcript)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: client proof: %v", ErrPairing, err)
	}
	payload, err := json.Marshal(pairingProof{Proof: clientProof})
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: encode client proof: %v", ErrPairing, err)
	}
	if err := pairing.WriteFrame(channel, pairing.Frame{Type: pairReplyFrame, Payload: payload}); err != nil {
		return PairResult{}, fmt.Errorf("%w: send client proof: %v", ErrPairing, err)
	}
	frame, err = readPairFrame(ctx, channel)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: receive result: %v", ErrPairing, err)
	}
	if frame.Type != pairCompleteFrame {
		return PairResult{}, fmt.Errorf("%w: expected completion", ErrPairingFrame)
	}
	var result PairResult
	if err := decodePairPayload(frame.Payload, &result); err != nil {
		return PairResult{}, err
	}
	if result.ServerID == "" || result.DeviceID == "" {
		return PairResult{}, fmt.Errorf("%w: incomplete result", ErrPairing)
	}
	return result, nil
}

func decodePairPayload(data []byte, target any) error {
	dec := json.NewDecoder(io.LimitReader(bytesReader(data), pairing.MaxFrameSize))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("%w: %v", ErrPairingFrame, err)
	}
	return nil
}

// bytesReader avoids exposing a mutable slice through a decoder helper.
func bytesReader(data []byte) io.Reader { return &pairBytes{data: append([]byte(nil), data...)} }

type pairBytes struct{ data []byte }

func (b *pairBytes) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func readPairFrame(ctx context.Context, channel io.ReadWriteCloser) (pairing.Frame, error) {
	result := make(chan struct {
		frame pairing.Frame
		err   error
	}, 1)
	go func() {
		frame, err := pairing.ReadFrame(channel)
		result <- struct {
			frame pairing.Frame
			err   error
		}{frame, err}
	}()
	select {
	case out := <-result:
		return out.frame, out.err
	case <-ctx.Done():
		_ = channel.Close()
		return pairing.Frame{}, ctx.Err()
	}
}

// NewTranscript creates the client half of a transcript with fresh nonces.
// The caller supplies the SSH session ID and the one-time phrase separately.
func NewTranscript(sessionID string, pin transport.HostPin, signer ssh.Signer, purpose pairing.Purpose, identifier string) (pairing.Transcript, error) {
	if signer == nil {
		return pairing.Transcript{}, fmt.Errorf("%w: signer is required", ErrPairing)
	}
	if sessionID == "" || pin.StableID == "" || pin.Fingerprint == "" {
		return pairing.Transcript{}, fmt.Errorf("%w: incomplete pin", ErrPairing)
	}
	clientNonce := make([]byte, 32)
	if _, err := rand.Read(clientNonce); err != nil {
		return pairing.Transcript{}, err
	}
	info, err := PublicKeyInfoFor(signer)
	if err != nil {
		return pairing.Transcript{}, err
	}
	return pairing.Transcript{SessionID: sessionID, ServerID: pin.StableID, HostKeyFingerprint: pin.Fingerprint,
		ClientNonce: clientNonce, ServerNonce: make([]byte, 32), PublicKey: info.Authorized,
		DeviceFingerprint: info.Fingerprint, Purpose: purpose, Identifier: identifier}, nil
}
