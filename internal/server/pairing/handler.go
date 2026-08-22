// Package pairingserver serves the bounded plumtree-pair-v1 SSH subsystem.
package pairingserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	protocol "github.com/Ceinl/plumtree/internal/protocol/pairing"
	"github.com/Ceinl/plumtree/internal/server/identity"
	"github.com/Ceinl/plumtree/internal/sqlite"
)

type Handler struct {
	Identity                                       *identity.Service
	ServerID, HostKeyAlgorithm, HostKeyFingerprint string
	ProductVersion                                 string
	SessionID                                      string
	CandidatePublicKey, CandidateFingerprint       string
}

func (h Handler) Serve(channel io.ReadWriteCloser) error {
	if channel == nil || h.Identity == nil || h.ServerID == "" || h.SessionID == "" || h.CandidatePublicKey == "" || h.CandidateFingerprint == "" {
		return errors.New("pairing server: incomplete handler")
	}
	serverHello := protocol.ServerHello{ServerID: h.ServerID, HostKeyAlgorithm: h.HostKeyAlgorithm, HostKeyFingerprint: h.HostKeyFingerprint,
		ProductVersion: h.ProductVersion, Protocols: []string{protocol.ProtocolName, "plumtree-control-v1"}}
	if err := writeJSONFrame(channel, protocol.FrameServerHello, serverHello); err != nil {
		return err
	}
	frame, err := protocol.ReadFrame(channel)
	if err != nil {
		return err
	}
	if frame.Type != protocol.FrameClientHello {
		return h.problem(channel, "invalid_frame", "expected client hello")
	}
	var hello protocol.ClientHello
	if err := decode(frame.Payload, &hello); err != nil {
		return h.problem(channel, "invalid_request", "client hello is invalid")
	}
	if err := h.validateTranscript(hello.Transcript); err != nil {
		return h.problem(channel, "transcript_mismatch", "pairing transcript does not match this SSH session: "+err.Error())
	}
	credential, handle, deviceName, err := h.credential(hello)
	if err != nil {
		return h.problem(channel, problemCode(err), "pairing authority is not available")
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	transcript := hello.Transcript
	transcript.ServerNonce = nonce
	proof, err := protocol.ServerProof(credential.Verifier, transcript)
	if err != nil {
		return h.problem(channel, "invalid_request", "pairing transcript is invalid")
	}
	if err := writeJSONFrame(channel, protocol.FrameServerProof, protocol.Proof{Salt: credential.Salt, ServerNonce: nonce, MAC: proof}); err != nil {
		return err
	}
	frame, err = protocol.ReadFrame(channel)
	if err != nil {
		return err
	}
	if frame.Type != protocol.FrameClientProof {
		return h.problem(channel, "invalid_frame", "expected client proof")
	}
	var reply protocol.Proof
	if err := decode(frame.Payload, &reply); err != nil || protocol.VerifyClientProof(credential.Verifier, transcript, reply.MAC) != nil {
		return h.problem(channel, "proof_mismatch", "pairing proof was rejected")
	}
	complete, err := h.complete(hello, credential, handle, deviceName)
	if err != nil {
		return h.problem(channel, problemCode(err), "pairing could not be completed")
	}
	return writeJSONFrame(channel, protocol.FrameComplete, complete)
}

func (h Handler) validateTranscript(value protocol.Transcript) error {
	if err := value.Validate(); err != nil {
		return err
	}
	for field, pair := range map[string][2]string{
		"session": {value.SessionID, h.SessionID}, "server": {value.ServerID, h.ServerID}, "host-key": {value.HostKeyFingerprint, h.HostKeyFingerprint},
		"public-key": {value.PublicKey, h.CandidatePublicKey}, "device-fingerprint": {value.DeviceFingerprint, h.CandidateFingerprint},
	} {
		if pair[0] != pair[1] {
			return fmt.Errorf("%w: %s", protocol.ErrInvalid, field)
		}
	}
	if !bytes.Equal(value.ServerNonce, make([]byte, len(value.ServerNonce))) {
		return protocol.ErrInvalid
	}
	return nil
}

func (h Handler) credential(hello protocol.ClientHello) (sqlite.PairingCredential, string, string, error) {
	switch hello.Transcript.Purpose {
	case protocol.PurposeNewAuthor:
		authority, err := h.Identity.BootstrapCredential(context.Background(), hello.Transcript.Identifier)
		if err != nil {
			return sqlite.PairingCredential{}, "", "", err
		}
		if len(hello.RecoverySalt) == 0 || len(hello.RecoveryVerifier) == 0 {
			return sqlite.PairingCredential{}, "", "", sqlite.ErrInvalid
		}
		return sqlite.PairingCredential{Salt: authority.Salt, Verifier: authority.Verifier}, authority.Handle, authority.DeviceName, nil
	case protocol.PurposeAddDevice:
		credential, err := h.Identity.EnrollmentCredential(context.Background(), hello.Transcript.Identifier)
		return credential, "", credential.DeviceName, err
	case protocol.PurposeOfflineRecovery:
		if strings.TrimSpace(hello.DeviceName) == "" || len(hello.RecoverySalt) == 0 || len(hello.RecoveryVerifier) == 0 {
			return sqlite.PairingCredential{}, "", "", sqlite.ErrInvalid
		}
		credential, err := h.Identity.RecoveryCredential(context.Background(), hello.Transcript.Identifier)
		return credential, credential.Handle, hello.DeviceName, err
	default:
		return sqlite.PairingCredential{}, "", "", sqlite.ErrInvalid
	}
}

func (h Handler) complete(hello protocol.ClientHello, credential sqlite.PairingCredential, handle, deviceName string) (protocol.Complete, error) {
	deviceID, err := randomID("device")
	if err != nil {
		return protocol.Complete{}, err
	}
	switch hello.Transcript.Purpose {
	case protocol.PurposeNewAuthor:
		authorID, err := randomID("author")
		if err != nil {
			return protocol.Complete{}, err
		}
		author, device, err := h.Identity.CompleteBootstrapRegistration(context.Background(), hello.Transcript.Identifier, credential.Verifier, sqlite.RegistrationInput{
			AuthorID: authorID, Handle: handle, DeviceID: deviceID, DeviceName: deviceName, PublicKey: hello.Transcript.PublicKey,
			Fingerprint: hello.Transcript.DeviceFingerprint, RecoverySalt: hello.RecoverySalt, RecoveryVerifier: hello.RecoveryVerifier,
		})
		if err != nil {
			return protocol.Complete{}, err
		}
		return protocol.Complete{ServerID: h.ServerID, AuthorID: author.ID, AuthorHandle: author.Handle, DeviceID: device.ID}, nil
	case protocol.PurposeAddDevice:
		device, err := h.Identity.CompleteDeviceAdditionVerifier(context.Background(), sqlite.DeviceEnrollmentInput{TokenID: hello.Transcript.Identifier, DeviceID: deviceID, PublicKey: hello.Transcript.PublicKey, Fingerprint: hello.Transcript.DeviceFingerprint, Verifier: credential.Verifier})
		if err != nil {
			return protocol.Complete{}, err
		}
		author, err := h.Identity.Author(context.Background(), device.AuthorID)
		if err != nil {
			return protocol.Complete{}, err
		}
		return protocol.Complete{ServerID: h.ServerID, AuthorID: author.ID, AuthorHandle: author.Handle, DeviceID: device.ID}, nil
	case protocol.PurposeOfflineRecovery:
		device, err := h.Identity.CompleteRecovery(context.Background(), sqlite.RecoveryInput{AuthorID: credential.AuthorID, CurrentVerifier: credential.Verifier,
			DeviceID: deviceID, DeviceName: deviceName, PublicKey: hello.Transcript.PublicKey, Fingerprint: hello.Transcript.DeviceFingerprint,
			NextSalt: hello.RecoverySalt, NextVerifier: hello.RecoveryVerifier, RevokeOldDevices: true})
		if err != nil {
			return protocol.Complete{}, err
		}
		return protocol.Complete{ServerID: h.ServerID, AuthorID: credential.AuthorID, AuthorHandle: handle, DeviceID: device.ID}, nil
	default:
		return protocol.Complete{}, sqlite.ErrInvalid
	}
}

func (h Handler) problem(channel io.Writer, code, detail string) error {
	return writeJSONFrame(channel, protocol.FrameProblem, protocol.Problem{Code: code, Detail: detail})
}

func problemCode(err error) string {
	switch {
	case errors.Is(err, sqlite.ErrNotFound):
		return "pairing_not_found"
	case errors.Is(err, sqlite.ErrConflict):
		return "pairing_conflict"
	case errors.Is(err, sqlite.ErrSuspended):
		return "pairing_suspended"
	default:
		return "pairing_rejected"
	}
}

func writeJSONFrame(w io.Writer, frameType byte, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return protocol.WriteFrame(w, protocol.Frame{Type: frameType, Payload: payload})
}

func decode(data []byte, target any) error {
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(data), protocol.MaxFrameSize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func randomID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("pairing server: random ID: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}
