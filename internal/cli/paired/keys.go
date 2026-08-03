package paired

import (
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

var (
	ErrInvalidKey      = errors.New("paired: invalid device key")
	ErrKeyExists       = errors.New("paired: device key already exists")
	ErrCredentialStore = errors.New("paired: credential store unavailable")
)

// FileKeyStore is the portable credential fallback. Files contain an
// OpenSSH-encoded Ed25519 private key and are always created with mode 0600.
// A platform keychain can implement the same KeyStore interface later without
// changing the server-record format.
type FileKeyStore struct {
	Dir string
}

type KeyStore interface {
	Generate(serverID string) (keyRef string, signer ssh.Signer, err error)
	Load(keyRef string) (ssh.Signer, error)
}

func (s FileKeyStore) keyPath(serverID string) (string, error) {
	if s.Dir == "" || !validName(serverID) {
		return "", fmt.Errorf("%w: invalid key store or server ID", ErrInvalidKey)
	}
	return filepath.Join(s.Dir, serverID+".ed25519"), nil
}

func (s FileKeyStore) Generate(serverID string) (string, ssh.Signer, error) {
	path, err := s.keyPath(serverID)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(s.Dir, defaultDirectoryMode); err != nil {
		return "", nil, err
	}
	if err := os.Chmod(s.Dir, defaultDirectoryMode); err != nil {
		return "", nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != privateFileMode {
			return "", nil, fmt.Errorf("%w: existing key has unsafe permissions", ErrInvalidKey)
		}
		return "", nil, ErrKeyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", nil, err
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "plumtree device key")
	if err != nil {
		return "", nil, fmt.Errorf("%w: marshal: %v", ErrInvalidKey, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", nil, ErrKeyExists
		}
		return "", nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(path)
		}
	}()
	if err := f.Chmod(privateFileMode); err != nil {
		return "", nil, err
	}
	if _, err := f.Write(pem.EncodeToMemory(block)); err != nil {
		return "", nil, err
	}
	if err := f.Sync(); err != nil {
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		return "", nil, err
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return "", nil, err
	}
	_ = publicKey // retain the explicit generation of the matching public key.
	ok = true
	return filepath.Base(path), signer, nil
}

func (s FileKeyStore) Load(keyRef string) (ssh.Signer, error) {
	if s.Dir == "" || keyRef == "" || filepath.IsAbs(keyRef) || filepath.Clean(keyRef) != keyRef || strings.HasPrefix(keyRef, "../") || keyRef == ".." {
		return nil, fmt.Errorf("%w: invalid key reference", ErrInvalidKey)
	}
	path := filepath.Join(s.Dir, keyRef)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != privateFileMode {
		return nil, fmt.Errorf("%w: key must be a regular 0600 file", ErrInvalidKey)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := ssh.ParseRawPrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("%w: parse: %v", ErrInvalidKey, err)
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		if pointer, pointerOK := key.(*ed25519.PrivateKey); pointerOK && pointer != nil {
			private = *pointer
			ok = true
		}
	}
	if !ok || len(private) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: key is not Ed25519", ErrInvalidKey)
	}
	return ssh.NewSignerFromKey(private)
}

type PublicKeyInfo struct {
	Algorithm   string
	Authorized  string
	Fingerprint string
}

func PublicKeyInfoFor(signer ssh.Signer) (PublicKeyInfo, error) {
	if signer == nil || signer.PublicKey() == nil {
		return PublicKeyInfo{}, ErrInvalidKey
	}
	key := signer.PublicKey()
	return PublicKeyInfo{
		Algorithm:   key.Type(),
		Authorized:  strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
		Fingerprint: ssh.FingerprintSHA256(key),
	}, nil
}

// FileCredentialStore makes the fallback explicit to callers that want to
// prefer an OS credential provider. It stores references, never secret bytes.
type FileCredentialStore struct{ Keys FileKeyStore }

func (s FileCredentialStore) Generate(serverID string) (string, ssh.Signer, error) {
	if s.Keys.Dir == "" {
		return "", nil, ErrCredentialStore
	}
	return s.Keys.Generate(serverID)
}

func (s FileCredentialStore) Load(ref string) (ssh.Signer, error) {
	if s.Keys.Dir == "" {
		return nil, ErrCredentialStore
	}
	return s.Keys.Load(ref)
}
