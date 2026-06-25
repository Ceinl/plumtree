package sshgateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

func devHostKey() (ssh.Signer, error) {
	gen := func() (ssh.Signer, error) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		return ssh.NewSignerFromSigner(priv)
	}

	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return gen()
	}
	path := filepath.Join(cfgDir, "plumtree", "dev_host_ed25519")
	if b, err := os.ReadFile(path); err == nil {
		if signer, err := ssh.ParsePrivateKey(b); err == nil {
			return signer, nil
		}
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if der, err := x509.MarshalPKCS8PrivateKey(priv); err == nil {
		blk := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			_ = os.WriteFile(path, blk, 0o600)
		}
	}
	return ssh.NewSignerFromSigner(priv)
}
