package paired

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Ceinl/plumtree/internal/protocol/pairing"
	"github.com/Ceinl/plumtree/internal/transport"
	"golang.org/x/crypto/ssh"
)

// PairInput is the noninteractive, deterministic input boundary for pairing.
// Secret is consumed by PairExchange and is never retained by Manager.
type PairInput struct {
	Host           string
	Port           int
	Name           string
	DeviceName     string
	ConfirmHostKey bool
	Purpose        pairing.Purpose
	Identifier     string
	Secret         []byte
}

type PairExchange func(context.Context, transport.HostPin, ssh.Signer, pairing.Purpose, string, []byte) (PairResult, error)

type Manager struct {
	StorePath string
	Keys      FileKeyStore
}

func (m Manager) load() (*Store, error) {
	if m.StorePath == "" {
		return nil, fmt.Errorf("%w: store path is required", ErrInvalidStore)
	}
	return Load(m.StorePath)
}

func (m Manager) save(store *Store) error { return Save(m.StorePath, store) }

// Pair discovers the SSH service, requires explicit first-use confirmation,
// creates one dedicated Ed25519 device key, completes the injected pairing
// exchange, and commits the record only after the server accepts the device.
func (m Manager) Pair(ctx context.Context, input PairInput, probe transport.Probe, exchange PairExchange) (ServerRecord, error) {
	if input.Purpose == "" {
		input.Purpose = pairing.PurposeNewAuthor
	}
	if len(input.Secret) < 16 {
		return ServerRecord{}, fmt.Errorf("%w: phrase must contain at least 16 bytes", ErrPairing)
	}
	endpoint, info, err := transport.Discover(ctx, input.Host, input.Port, probe)
	if err != nil {
		return ServerRecord{}, err
	}
	pin, err := transport.FirstUsePin(endpoint, info, input.ConfirmHostKey)
	if err != nil {
		return ServerRecord{}, err
	}
	if exchange == nil {
		return ServerRecord{}, fmt.Errorf("%w: pairing exchange is required", ErrPairing)
	}
	secret := append([]byte(nil), input.Secret...)
	defer func() {
		for i := range secret {
			secret[i] = 0
		}
	}()
	keyRef, signer, err := m.Keys.Generate(info.StableID)
	if err != nil {
		return ServerRecord{}, err
	}
	keepKey := false
	defer func() {
		if !keepKey {
			_ = os.Remove(filepath.Join(m.Keys.Dir, keyRef))
		}
	}()
	result, err := exchange(ctx, pin, signer, input.Purpose, input.Identifier, secret)
	if err != nil {
		return ServerRecord{}, err
	}
	if result.ServerID != pin.StableID {
		return ServerRecord{}, fmt.Errorf("%w: server identity changed", transport.ErrServerIDChanged)
	}
	if result.DeviceID == "" {
		return ServerRecord{}, fmt.Errorf("%w: server did not return device identity", ErrPairing)
	}
	name := input.Name
	if name == "" {
		name = input.Host
	}
	record := ServerRecord{Name: name, ServerID: pin.StableID, Host: endpoint.Host, Port: endpoint.Port,
		HostKeyAlgorithm: pin.Algorithm, HostKeyFingerprint: pin.Fingerprint, ProductVersion: pin.ProductVersion,
		AuthorID: result.AuthorID, AuthorHandle: result.AuthorHandle, DeviceID: result.DeviceID,
		DeviceName: input.DeviceName, KeyRef: keyRef}
	store, err := m.load()
	if err != nil {
		return ServerRecord{}, err
	}
	if err := store.Add(record); err != nil {
		return ServerRecord{}, err
	}
	if err := m.save(store); err != nil {
		return ServerRecord{}, err
	}
	keepKey = true
	return record, nil
}

// Recover is the same transactional operation as Pair, with the recovery
// purpose made explicit for transcript construction and server policy.
func (m Manager) Recover(ctx context.Context, input PairInput, probe transport.Probe, exchange PairExchange) (ServerRecord, error) {
	input.Purpose = pairing.PurposeOfflineRecovery
	return m.Pair(ctx, input, probe, exchange)
}

func (m Manager) Switch(name string) error {
	store, err := m.load()
	if err != nil {
		return err
	}
	if err := store.Select(name); err != nil {
		return err
	}
	return m.save(store)
}

func (m Manager) Rename(oldName, newName string) error {
	store, err := m.load()
	if err != nil {
		return err
	}
	if err := store.Rename(oldName, newName); err != nil {
		return err
	}
	return m.save(store)
}

func (m Manager) Unpair(ctx context.Context, name string, revoke func(context.Context, ServerRecord) error) error {
	store, err := m.load()
	if err != nil {
		return err
	}
	record, err := store.Get(name)
	if err != nil {
		return err
	}
	if err := Unpair(ctx, store, name, false, revoke); err != nil {
		return err
	}
	if err := m.save(store); err != nil {
		_ = store.Add(record)
		return err
	}
	return os.Remove(filepath.Join(m.Keys.Dir, record.KeyRef))
}

// Forget is intentionally separate from Unpair: it removes local trust after
// an unreachable server or local recovery event and sends no remote request.
func (m Manager) Forget(name string) error {
	store, err := m.load()
	if err != nil {
		return err
	}
	record, err := store.Remove(name)
	if err != nil {
		return err
	}
	if err := m.save(store); err != nil {
		_ = store.Add(record)
		return err
	}
	if err := os.Remove(filepath.Join(m.Keys.Dir, record.KeyRef)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m Manager) Current() (ServerRecord, error) {
	store, err := m.load()
	if err != nil {
		return ServerRecord{}, err
	}
	return store.CurrentRecord()
}

func DevicePublicKey(signer ssh.Signer) (ed25519.PublicKey, error) {
	if signer == nil || signer.PublicKey() == nil {
		return nil, ErrInvalidKey
	}
	key, ok := signer.PublicKey().(ssh.CryptoPublicKey)
	if !ok {
		return nil, ErrInvalidKey
	}
	public, ok := key.CryptoPublicKey().(ed25519.PublicKey)
	if !ok {
		return nil, ErrInvalidKey
	}
	return append(ed25519.PublicKey(nil), public...), nil
}
