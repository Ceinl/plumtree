// Package hostkey centralizes SSH host-key handling: strict loading of
// operator-supplied keys, generation, and atomic persistence. A persisted host
// key pins server identity for SSH clients (trust on first use), so an
// existing-but-unusable key file is always a hard error — silently regenerating
// it would break every client's pin.
package hostkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Ceinl/plumtree/internal/fsatomic"
	"golang.org/x/crypto/ssh"
)

// Load reads and parses the host key at path. A missing or unparseable file is
// a hard error naming the path; callers never regenerate operator-supplied keys.
func Load(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("host key %s: %w", path, err)
	}
	return parse(path, data)
}

// LoadOrCreate returns the persisted host key at path, generating a fresh
// ed25519 key and persisting it atomically only when the file genuinely does
// not exist. An existing but unreadable or unparseable file is a hard error so
// clients' trust-on-first-use pins stay valid.
func LoadOrCreate(path, comment string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return parse(path, data)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("host key %s: %w", path, err)
	}
	signer, pemBytes, err := Generate(comment)
	if err != nil {
		return nil, err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("host key directory %s: %w", dir, err)
		}
	}
	if err := fsatomic.WriteFileAtomic(path, pemBytes, 0o600); err != nil {
		return nil, fmt.Errorf("persist host key %s: %w", path, err)
	}
	return signer, nil
}

// Generate creates a fresh ed25519 host key and returns its signer together
// with the PEM encoding persisted for later runs.
func Generate(comment string) (ssh.Signer, []byte, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	block, err := ssh.MarshalPrivateKey(privateKey, comment)
	if err != nil {
		return nil, nil, err
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, nil, err
	}
	return signer, pem.EncodeToMemory(block), nil
}

func parse(path string, data []byte) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("host key %s exists but cannot be parsed (%v); remove the file to regenerate it — every client pins this key", path, err)
	}
	return signer, nil
}
