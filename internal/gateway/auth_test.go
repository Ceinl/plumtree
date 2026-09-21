package gateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

type mismatchedSigner struct {
	public ssh.PublicKey
	signer ssh.Signer
}

func (s mismatchedSigner) PublicKey() ssh.PublicKey { return s.public }
func (s mismatchedSigner) Sign(r io.Reader, data []byte) (*ssh.Signature, error) {
	return s.signer.Sign(r, data)
}

func TestOptionalAuthRequiresProofBeforeRecordingFingerprint(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := signer.PublicKey()
	cfg := optionalAuthConfig()

	if cfg.NoClientAuth {
		t.Fatal("none authentication must not bypass optional public-key auth")
	}
	permissions, err := cfg.PublicKeyCallback(nil, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := permissions.Extensions["pubkey-fp"]; got != "" {
		t.Fatalf("unsigned key query recorded fingerprint %q", got)
	}

	permissions, err = cfg.VerifiedPublicKeyCallback(nil, publicKey, permissions, publicKey.Type())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := permissions.Extensions["pubkey-fp"], ssh.FingerprintSHA256(publicKey); got != want {
		t.Fatalf("verified fingerprint = %q, want %q", got, want)
	}
}

func TestOptionalAuthHasExplicitAnonymousFallback(t *testing.T) {
	cfg := optionalAuthConfig()
	permissions, err := cfg.KeyboardInteractiveCallback(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := permissions.Extensions["auth-kind"]; got != "anonymous" {
		t.Fatalf("auth kind = %q, want anonymous", got)
	}
	if got := permissions.Extensions["pubkey-fp"]; got != "" {
		t.Fatalf("anonymous auth recorded fingerprint %q", got)
	}
}

func TestOptionalAuthHandshakeDoesNotTrustInvalidKeyProof(t *testing.T) {
	valid := newTestSigner(t)
	other := newTestSigner(t)
	invalid := mismatchedSigner{public: valid.PublicKey(), signer: other}

	permissions, err := testHandshakeResult(t, []ssh.AuthMethod{
		ssh.PublicKeys(invalid),
		ssh.KeyboardInteractive(func(_, _ string, _ []string, _ []bool) ([]string, error) {
			return nil, nil
		}),
	})
	if err == nil {
		t.Fatal("invalid key proof completed the SSH handshake")
	}
	if permissions != nil && permissions.Extensions["pubkey-fp"] != "" {
		got := permissions.Extensions["pubkey-fp"]
		t.Fatalf("invalid key proof claimed fingerprint %q", got)
	}
}

func TestOptionalAuthHandshakeRecordsValidKeyProof(t *testing.T) {
	signer := newTestSigner(t)
	permissions := testHandshake(t, []ssh.AuthMethod{ssh.PublicKeys(signer)})
	if got, want := permissions.Extensions["pubkey-fp"], ssh.FingerprintSHA256(signer.PublicKey()); got != want {
		t.Fatalf("proved fingerprint = %q, want %q", got, want)
	}
}

func TestOptionalAuthHandshakeAllowsAnonymous(t *testing.T) {
	permissions := testHandshake(t, []ssh.AuthMethod{
		ssh.KeyboardInteractive(func(_, _ string, _ []string, _ []bool) ([]string, error) {
			return nil, nil
		}),
	})
	if got := permissions.Extensions["auth-kind"]; got != "anonymous" {
		t.Fatalf("auth kind = %q, want anonymous", got)
	}
	if got := permissions.Extensions["pubkey-fp"]; got != "" {
		t.Fatalf("anonymous handshake claimed fingerprint %q", got)
	}
}

func newTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func testHandshake(t *testing.T, auth []ssh.AuthMethod) *ssh.Permissions {
	t.Helper()
	permissions, err := testHandshakeResult(t, auth)
	if err != nil {
		t.Fatal(err)
	}
	return permissions
}

func testHandshakeResult(t *testing.T, auth []ssh.AuthMethod) (*ssh.Permissions, error) {
	t.Helper()
	serverConfig := optionalAuthConfig()
	serverConfig.AddHostKey(newTestSigner(t))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	result := make(chan struct {
		permissions *ssh.Permissions
		err         error
	}, 1)
	go func() {
		serverSide, err := listener.Accept()
		if err != nil {
			result <- struct {
				permissions *ssh.Permissions
				err         error
			}{err: err}
			return
		}
		defer serverSide.Close()
		conn, _, _, err := ssh.NewServerConn(serverSide, serverConfig)
		if err != nil {
			result <- struct {
				permissions *ssh.Permissions
				err         error
			}{err: err}
			return
		}
		result <- struct {
			permissions *ssh.Permissions
			err         error
		}{permissions: conn.Permissions}
		_ = conn.Close()
	}()
	clientConfig := &ssh.ClientConfig{
		User:            "app",
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	clientSide, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientSide.Close()
	conn, _, _, err := ssh.NewClientConn(clientSide, "pipe", clientConfig)
	if err != nil {
		serverResult := <-result
		if serverResult.permissions != nil {
			return serverResult.permissions, err
		}
		return nil, err
	}
	_ = conn.Close()
	got := <-result
	if got.err != nil {
		return nil, got.err
	}
	return got.permissions, nil
}
