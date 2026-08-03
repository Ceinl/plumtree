package control

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
)

const envelopeFormat = "plumtree-envelope-v1"

// envelopeCipher encrypts every persisted snapshot with a fresh data-encryption
// key (DEK). The DEK is stored beside the ciphertext only after being wrapped
// by the caller-supplied key-encryption key (KEK), which must live outside the
// data volume.
type envelopeCipher struct{ kek []byte }

type encryptedSnapshot struct {
	Format          string `json:"format"`
	WrappedKeyNonce []byte `json:"wrappedKeyNonce"`
	WrappedKey      []byte `json:"wrappedKey"`
	DataNonce       []byte `json:"dataNonce"`
	Ciphertext      []byte `json:"ciphertext"`
}

func newEnvelopeCipher(kek []byte) (*envelopeCipher, error) {
	if len(kek) != 32 {
		return nil, fmt.Errorf("snapshot encryption key must be exactly 32 bytes")
	}
	return &envelopeCipher{kek: append([]byte(nil), kek...)}, nil
}

func (c *envelopeCipher) encrypt(plaintext []byte) ([]byte, error) {
	dek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, err
	}
	dataNonce, ciphertext, err := seal(dek, plaintext)
	if err != nil {
		return nil, err
	}
	wrapNonce, wrappedKey, err := seal(c.kek, dek)
	if err != nil {
		return nil, err
	}
	return json.Marshal(encryptedSnapshot{Format: envelopeFormat, WrappedKeyNonce: wrapNonce, WrappedKey: wrappedKey, DataNonce: dataNonce, Ciphertext: ciphertext})
}

// decrypt returns the input untouched for a legacy plaintext snapshot so it can
// be migrated atomically on startup after the JSON has been validated.
func (c *envelopeCipher) decrypt(raw []byte) ([]byte, bool, error) {
	var envelope encryptedSnapshot
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Format != envelopeFormat {
		return raw, false, nil
	}
	dek, err := openSealed(c.kek, envelope.WrappedKeyNonce, envelope.WrappedKey)
	if err != nil {
		return nil, true, fmt.Errorf("unwrap snapshot data key: %w", err)
	}
	plaintext, err := openSealed(dek, envelope.DataNonce, envelope.Ciphertext)
	if err != nil {
		return nil, true, fmt.Errorf("decrypt snapshot: %w", err)
	}
	return plaintext, true, nil
}

func seal(key, plaintext []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, plaintext, nil), nil
}

func openSealed(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("invalid nonce length")
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}
