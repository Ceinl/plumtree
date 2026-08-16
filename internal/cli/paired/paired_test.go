package paired

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/protocol/pairing"
	"github.com/Ceinl/plumtree/internal/transport"
	"golang.org/x/crypto/ssh"
)

func testRecord(name, id, keyRef string) ServerRecord {
	return ServerRecord{Name: name, ServerID: id, Host: "server.test", Port: 2222,
		HostKeyAlgorithm: "ssh-ed25519", HostKeyFingerprint: "SHA256:host", ProductVersion: "v1", KeyRef: keyRef}
}

func TestStoreIsPrivateAndSupportsIsolationSwitchRenameAndOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg", "servers.json")
	store := NewStore()
	if err := store.Add(testRecord("alpha", "a", "a.ed25519")); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(testRecord("beta", "b", "b.ed25519")); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, store); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("store mode=%v err=%v", info.Mode().Perm(), err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.Get("access-token"); !errors.Is(err, ErrServerNotFound) {
		t.Fatal(err)
	}
	if err := loaded.Select("beta"); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Rename("beta", "prod"); err != nil {
		t.Fatal(err)
	}
	target, err := loaded.ResolveTarget("prod", &transport.Endpoint{Host: "override.test", Port: 2200})
	if err != nil || target.ServerID != "b" || target.Endpoint.Host != "override.test" {
		t.Fatalf("target=%+v err=%v", target, err)
	}
	if got, err := loaded.CurrentRecord(); err != nil || got.Name != "prod" {
		t.Fatalf("current=%+v err=%v", got, err)
	}
	redacted, err := json.Marshal(loaded.Servers[0].Redacted())
	if err != nil || string(redacted) == "" || string(redacted) == string(mustJSON(loaded.Servers[0])) {
		t.Fatalf("redaction failed: %s", redacted)
	}
}

func TestFileKeyStoreUsesEd25519AndRejectsUnsafeFiles(t *testing.T) {
	keys := FileKeyStore{Dir: t.TempDir()}
	ref, signer, err := keys.Generate("server-1")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "server-1.ed25519" {
		t.Fatalf("ref=%q", ref)
	}
	loaded, err := keys.Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	if ssh.FingerprintSHA256(loaded.PublicKey()) != ssh.FingerprintSHA256(signer.PublicKey()) {
		t.Fatal("loaded key differs")
	}
	if _, _, err := keys.Generate("server-1"); !errors.Is(err, ErrKeyExists) {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(keys.Dir, ref), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Load(ref); !errors.Is(err, ErrInvalidKey) {
		t.Fatal(err)
	}
}

func TestPairingExchangeBindsServerNonceAndProof(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	_, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	pin := transport.HostPin{StableID: "server", Fingerprint: "SHA256:host", ProductVersion: "v1"}
	transcript, err := NewTranscript("session", pin, signer, pairing.PurposeNewAuthor, "alice")
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("0123456789abcdef-one-use")
	serverDone := make(chan error, 1)
	go func() {
		frame, err := pairing.ReadFrame(right)
		if err != nil {
			serverDone <- err
			return
		}
		var hello pairingHello
		if err := json.Unmarshal(frame.Payload, &hello); err != nil {
			serverDone <- err
			return
		}
		hello.Transcript.ServerNonce = []byte("server-nonce-0123456789")
		proof, err := pairing.ServerProof(secret, hello.Transcript)
		if err != nil {
			serverDone <- err
			return
		}
		payload, _ := json.Marshal(pairingProof{ServerNonce: hello.Transcript.ServerNonce, Proof: proof})
		if err := pairing.WriteFrame(right, pairing.Frame{Type: pairProofFrame, Payload: payload}); err != nil {
			serverDone <- err
			return
		}
		frame, err = pairing.ReadFrame(right)
		if err != nil {
			serverDone <- err
			return
		}
		var clientProof pairingProof
		if err := json.Unmarshal(frame.Payload, &clientProof); err != nil {
			serverDone <- err
			return
		}
		if err := pairing.VerifyClientProof(secret, hello.Transcript, clientProof.Proof); err != nil {
			serverDone <- err
			return
		}
		result, _ := json.Marshal(PairResult{ServerID: "server", DeviceID: "device"})
		serverDone <- pairing.WriteFrame(right, pairing.Frame{Type: pairCompleteFrame, Payload: result})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := ExchangePairing(ctx, left, transcript, secret)
	if err != nil || result.ServerID != "server" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := <-serverDone; err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}
