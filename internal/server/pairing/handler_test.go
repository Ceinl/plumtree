package pairingserver_test

import (
	"context"
	"crypto/ed25519"
	"net"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/cli/paired"
	protocol "github.com/Ceinl/plumtree/internal/protocol/pairing"
	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	"github.com/Ceinl/plumtree/internal/server/identity"
	pairingserver "github.com/Ceinl/plumtree/internal/server/pairing"
	"github.com/Ceinl/plumtree/internal/sqlite"
	"github.com/Ceinl/plumtree/internal/transport"
	"golang.org/x/crypto/ssh"
)

func TestCandidateCanConsumeBootstrapAuthorityOnce(t *testing.T) {
	repo, err := sqlite.OpenRepository(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	bootstrapSecret := []byte("bootstrap-secret-0123456789")
	salt := []byte("bootstrap-salt")
	verifier, _ := protocol.DeriveVerifier(salt, bootstrapSecret)
	_, err = repo.CreateBootstrapAuthority(context.Background(), sqlite.BootstrapAuthorityInput{ID: "bootstrap-1", Handle: "alice", DeviceName: "laptop", Salt: salt, Verifier: verifier, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	_, private, _ := ed25519.GenerateKey(nil)
	signer, _ := ssh.NewSignerFromKey(private)
	key, _ := paired.PublicKeyInfoFor(signer)
	cfg := serverconfig.Default()
	cfg.Roles.Control = true
	cfg.Exposure.SSH = serverconfig.ExposureGate{Enabled: true, Address: "local"}
	identities, err := identity.New(repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	handler := pairingserver.Handler{Identity: identities, ServerID: "server-1", HostKeyAlgorithm: "ssh-ed25519", HostKeyFingerprint: "SHA256:host", ProductVersion: "dev", SessionID: "session-1", CandidatePublicKey: key.Authorized, CandidateFingerprint: key.Fingerprint}
	left, right := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- handler.Serve(right) }()
	if _, err := paired.ReadServerHello(context.Background(), left); err != nil {
		t.Fatal(err)
	}

	transcript, err := paired.NewTranscript("session-1", transport.HostPin{StableID: "server-1", Fingerprint: "SHA256:host", ProductVersion: "dev"}, signer, protocol.PurposeNewAuthor, "bootstrap-1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := paired.ExchangePairing(context.Background(), left, transcript, bootstrapSecret, paired.ExchangeOptions{DeviceName: "laptop", RecoverySecret: []byte("offline-recovery-secret-012345")})
	if err != nil || result.AuthorHandle != "alice" || result.DeviceID == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DeviceByFingerprint(context.Background(), key.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BootstrapAuthorityCredential(context.Background(), "bootstrap-1"); err == nil {
		t.Fatal("consumed bootstrap authority remains usable")
	}
}
