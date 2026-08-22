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

var (
	ErrPairing      = errors.New("paired: pairing failed")
	ErrPairingFrame = errors.New("paired: invalid pairing frame")
)

type PairResult = pairing.Complete

type ExchangeOptions struct {
	DeviceName       string
	RecoverySecret   []byte
	RevokeOldDevices bool
}

func ReadServerHello(ctx context.Context, channel io.ReadWriteCloser) (pairing.ServerHello, error) {
	frame, err := readPairFrame(ctx, channel)
	if err != nil {
		return pairing.ServerHello{}, fmt.Errorf("%w: server hello: %v", ErrPairing, err)
	}
	if frame.Type != pairing.FrameServerHello {
		return pairing.ServerHello{}, fmt.Errorf("%w: expected server hello", ErrPairingFrame)
	}
	var hello pairing.ServerHello
	if err := decodePairPayload(frame.Payload, &hello); err != nil {
		return pairing.ServerHello{}, err
	}
	if hello.ServerID == "" || hello.HostKeyAlgorithm == "" || hello.HostKeyFingerprint == "" || hello.ProductVersion == "" {
		return pairing.ServerHello{}, fmt.Errorf("%w: incomplete server hello", ErrPairingFrame)
	}
	return hello, nil
}

// ExchangePairing runs the proof-only exchange on an already-authenticated
// plumtree-pair-v1 channel. The phrase is used only to derive MAC keys and is
// never serialized, persisted, or included in errors.
func ExchangePairing(ctx context.Context, channel io.ReadWriteCloser, transcript pairing.Transcript, secret []byte, options ...ExchangeOptions) (PairResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if channel == nil {
		return PairResult{}, fmt.Errorf("%w: nil channel", ErrPairing)
	}
	if err := transcript.Validate(); err != nil {
		return PairResult{}, fmt.Errorf("%w: %v", ErrPairing, err)
	}
	var option ExchangeOptions
	if len(options) > 0 {
		option = options[0]
	}
	var recoverySalt, recoveryVerifier []byte
	if transcript.Purpose == pairing.PurposeNewAuthor || transcript.Purpose == pairing.PurposeOfflineRecovery || transcript.Purpose == pairing.PurposeOperatorRecovery {
		if len(option.RecoverySecret) < 16 {
			return PairResult{}, fmt.Errorf("%w: next recovery phrase is required", ErrPairing)
		}
		recoverySalt = make([]byte, 16)
		if _, err := rand.Read(recoverySalt); err != nil {
			return PairResult{}, fmt.Errorf("%w: recovery salt: %v", ErrPairing, err)
		}
		var err error
		recoveryVerifier, err = pairing.DeriveVerifier(recoverySalt, option.RecoverySecret)
		if err != nil {
			return PairResult{}, fmt.Errorf("%w: recovery verifier: %v", ErrPairing, err)
		}
	}
	hello, err := json.Marshal(pairing.ClientHello{Transcript: transcript, DeviceName: option.DeviceName, RecoverySalt: recoverySalt, RecoveryVerifier: recoveryVerifier})
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: hello: %v", ErrPairing, err)
	}
	if err := pairing.WriteFrame(channel, pairing.Frame{Type: pairing.FrameClientHello, Payload: hello}); err != nil {
		return PairResult{}, fmt.Errorf("%w: send hello: %v", ErrPairing, err)
	}
	frame, err := readPairFrame(ctx, channel)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: receive proof: %v", ErrPairing, err)
	}
	if frame.Type == pairing.FrameProblem {
		return PairResult{}, decodePairProblem(frame.Payload)
	}
	if frame.Type != pairing.FrameServerProof {
		return PairResult{}, fmt.Errorf("%w: expected server proof", ErrPairingFrame)
	}
	var proof pairing.Proof
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
	verifier, err := pairing.DeriveVerifier(proof.Salt, secret)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: proof verifier: %v", ErrPairing, err)
	}
	if err := pairing.VerifyServerProof(verifier, transcript, proof.MAC); err != nil {
		return PairResult{}, fmt.Errorf("%w: server proof: %v", ErrPairing, err)
	}
	clientProof, err := pairing.ClientProof(verifier, transcript)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: client proof: %v", ErrPairing, err)
	}
	payload, err := json.Marshal(pairing.Proof{MAC: clientProof})
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: encode client proof: %v", ErrPairing, err)
	}
	if err := pairing.WriteFrame(channel, pairing.Frame{Type: pairing.FrameClientProof, Payload: payload}); err != nil {
		return PairResult{}, fmt.Errorf("%w: send client proof: %v", ErrPairing, err)
	}
	frame, err = readPairFrame(ctx, channel)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: receive result: %v", ErrPairing, err)
	}
	if frame.Type == pairing.FrameProblem {
		return PairResult{}, decodePairProblem(frame.Payload)
	}
	if frame.Type != pairing.FrameComplete {
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

func decodePairProblem(data []byte) error {
	var problem pairing.Problem
	if err := decodePairPayload(data, &problem); err != nil {
		return err
	}
	if problem.Code == "" {
		problem.Code = "pairing_failed"
	}
	return fmt.Errorf("%w: %s: %s", ErrPairing, problem.Code, problem.Detail)
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
