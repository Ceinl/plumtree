package pairing

import (
	"bytes"
	"errors"
	"testing"
)

func testTranscript() Transcript {
	return Transcript{SessionID: "session", ServerID: "server", HostKeyFingerprint: "SHA256:host", ClientNonce: bytes.Repeat([]byte{1}, 32), ServerNonce: bytes.Repeat([]byte{2}, 32), PublicKey: "ssh-ed25519 AAAA", DeviceFingerprint: "SHA256:device", Purpose: PurposeAddDevice, Identifier: "token"}
}

func TestTranscriptProofBindsAllFields(t *testing.T) {
	secret := bytes.Repeat([]byte("s"), 32)
	transcript := testTranscript()
	server, err := ServerProof(secret, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyServerProof(secret, transcript, server); err != nil {
		t.Fatal(err)
	}
	transcript.DeviceFingerprint = "SHA256:other"
	if err := VerifyServerProof(secret, transcript, server); !errors.Is(err, ErrProofMismatch) {
		t.Fatalf("mutated transcript accepted: %v", err)
	}
	client, err := ClientProof(secret, testTranscript())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyServerProof(secret, testTranscript(), client); !errors.Is(err, ErrProofMismatch) {
		t.Fatalf("reflected proof accepted: %v", err)
	}
}

func TestBoundedFrames(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, Frame{Type: 4, Payload: []byte("hello")}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&buf)
	if err != nil || got.Type != 4 || string(got.Payload) != "hello" {
		t.Fatalf("frame=%+v err=%v", got, err)
	}
	if err := WriteFrame(&buf, Frame{Payload: make([]byte, MaxFrameSize)}); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized frame=%v", err)
	}
}
