package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const storeSnapshotVersion = 1

type storeSnapshot struct {
	Version    int               `json:"version"`
	Seq        map[string]int    `json:"seq,omitempty"`
	Owners     []Owner           `json:"owners,omitempty"`
	Identities []AuthIdentity    `json:"identities,omitempty"`
	Apps       []App             `json:"apps,omitempty"`
	Artifacts  []Artifact        `json:"artifacts,omitempty"`
	Blobs      map[string][]byte `json:"blobs,omitempty"`
	Deploys    []Deploy          `json:"deploys,omitempty"`
	SSHKeys    []SSHKey          `json:"sshKeys,omitempty"`
	CITokens   []CIToken         `json:"ciTokens,omitempty"`
	Secrets    []SecretMetadata  `json:"secrets,omitempty"`
	// SecretValues persists the actual secret bytes alongside the value-free
	// Secrets metadata. They are local-server state and never leave the platform.
	SecretValues []persistedSecretValue `json:"secretValues,omitempty"`
	// EgressAllow maps app ID to its outbound-HTTP host allowlist.
	EgressAllow map[string][]string `json:"egressAllow,omitempty"`
	Sessions    []Session           `json:"sessions,omitempty"`
	Quotas      map[string]Quotas   `json:"quotas,omitempty"`
	// SuspendedDeploys lists deploy IDs under the deploy-level kill switch.
	SuspendedDeploys []string `json:"suspendedDeploys,omitempty"`
}

// persistedSecretValue is a secret's bytes keyed by app and name, for snapshot
// persistence only. []byte marshals as base64, keeping arbitrary values safe.
type persistedSecretValue struct {
	AppID string `json:"appId"`
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

// OpenStore returns a store backed by snapshotPath. A missing path starts empty;
// subsequent durable mutations atomically rewrite the snapshot file.
func OpenStore(snapshotPath string, opts ...Option) (*Store, error) {
	s := NewStore(opts...)
	if len(s.snapshotKey) > 0 {
		c, err := newEnvelopeCipher(s.snapshotKey)
		if err != nil {
			return nil, err
		}
		s.snapshotCipher = c
		s.writeSnapshot = func(path string, snap storeSnapshot) error {
			return writeEncryptedSnapshotFile(path, snap, c)
		}
	}
	if len(s.previousSnapshotKey) > 0 {
		c, err := newEnvelopeCipher(s.previousSnapshotKey)
		if err != nil {
			return nil, err
		}
		s.previousSnapshotCipher = c
	}
	if snapshotPath == "" {
		return s, nil
	}
	if err := s.LoadSnapshot(snapshotPath); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.persistPath = snapshotPath
	s.mu.Unlock()
	return s, nil
}

// LoadSnapshot replaces the current store contents with the snapshot at path.
// Missing files are treated as an empty store.
func (s *Store) LoadSnapshot(path string) error {
	if path == "" {
		return fmt.Errorf("%w: snapshot path is required", ErrInvalid)
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	wasEncrypted := false
	if s.snapshotCipher != nil {
		raw := b
		b, wasEncrypted, err = s.snapshotCipher.decrypt(raw)
		if err != nil {
			if s.previousSnapshotCipher == nil {
				return fmt.Errorf("decrypt control-plane snapshot %q: %w", path, err)
			}
			b, wasEncrypted, err = s.previousSnapshotCipher.decrypt(raw)
			if err != nil {
				return fmt.Errorf("decrypt control-plane snapshot %q with current or previous key: %w", path, err)
			}
			// The primary key could not read it, so atomically rewrap it below.
			wasEncrypted = false
		}
	}
	var snap storeSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return fmt.Errorf("load control-plane snapshot %q: %w", path, err)
	}
	loaded := NewStore(WithClock(s.now))
	// Reuse the configured blob store so a filesystem (durable) store keeps its
	// on-disk bytes; restore replays only the snapshot-embedded blobs (none for a
	// durable store, since its files persist independently).
	loaded.blobs = s.blobs
	migrated, err := loaded.restoreSnapshot(snap)
	if err != nil {
		return fmt.Errorf("load control-plane snapshot %q: %w", path, err)
	}
	if migrated || (s.snapshotCipher != nil && !wasEncrypted) {
		if err := s.writeSnapshot(path, loaded.snapshotLocked()); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	persistPath := s.persistPath
	s.seq = loaded.seq
	s.owners = loaded.owners
	s.ownerByHandle = loaded.ownerByHandle
	s.identities = loaded.identities
	s.apps = loaded.apps
	s.appByOwnerName = loaded.appByOwnerName
	s.artifacts = loaded.artifacts
	s.blobs = loaded.blobs
	s.deploys = loaded.deploys
	s.sshKeys = loaded.sshKeys
	s.sshKeyByFingerprint = loaded.sshKeyByFingerprint
	s.ciTokens = loaded.ciTokens
	s.secrets = loaded.secrets
	s.secretValues = loaded.secretValues
	s.egressAllow = loaded.egressAllow
	s.sessions = loaded.sessions
	s.quotas = loaded.quotas
	s.suspendedDeploys = loaded.suspendedDeploys
	s.persistPath = persistPath
	s.committedSnapshot = s.snapshotLocked()
	return nil
}

func (s *Store) persistLocked() error {
	candidate := s.snapshotLocked()
	if s.persistPath == "" {
		// Keep an in-memory checkpoint too. Besides making an in-memory store
		// internally consistent, this gives a store that later gains a durable
		// path a correct rollback point.
		s.committedSnapshot = candidate
		return nil
	}
	// snapshotLocked makes a detached candidate. The live maps remain private
	// under s.mu until the atomic replacement has succeeded.
	if err := s.writeSnapshot(s.persistPath, candidate); err != nil {
		s.restoreCommittedLocked()
		return err
	}
	s.committedSnapshot = candidate
	return nil
}

// restoreCommittedLocked rolls a failed durable mutation back to the last
// successfully persisted checkpoint. It is deliberately map-level rather than
// reloading the file, avoiding another I/O failure on the error path.
// s.mu must be held by the caller.
func (s *Store) restoreCommittedLocked() {
	restored := NewStore(WithClock(s.now))
	// The snapshot owns in-memory blob bytes. Recreate that store before replay
	// so blobs introduced by the failed mutation do not survive the rollback.
	if _, ok := s.blobs.(*memBlobStore); ok {
		restored.blobs = newMemBlobStore()
	} else {
		restored.blobs = s.blobs
	}
	if _, err := restored.restoreSnapshot(s.committedSnapshot); err != nil {
		// committedSnapshot was produced by snapshotLocked after a successful
		// write, so this is unreachable. Keep the original state rather than
		// replacing it with a partial restore if that invariant is ever broken.
		return
	}
	s.seq = restored.seq
	s.owners = restored.owners
	s.ownerByHandle = restored.ownerByHandle
	s.identities = restored.identities
	s.apps = restored.apps
	s.appByOwnerName = restored.appByOwnerName
	s.artifacts = restored.artifacts
	s.blobs = restored.blobs
	s.deploys = restored.deploys
	s.sshKeys = restored.sshKeys
	s.sshKeyByFingerprint = restored.sshKeyByFingerprint
	s.ciTokens = restored.ciTokens
	s.secrets = restored.secrets
	s.secretValues = restored.secretValues
	s.egressAllow = restored.egressAllow
	s.sessions = restored.sessions
	s.quotas = restored.quotas
	s.suspendedDeploys = restored.suspendedDeploys
}

func writeSnapshotFile(path string, snap storeSnapshot) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".plumtree-state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func writeEncryptedSnapshotFile(path string, snap storeSnapshot, c *envelopeCipher) error {
	var plaintext bytes.Buffer
	enc := json.NewEncoder(&plaintext)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		return err
	}
	ciphertext, err := c.encrypt(plaintext.Bytes())
	if err != nil {
		return fmt.Errorf("encrypt control-plane snapshot: %w", err)
	}
	return writeSnapshotBytes(path, ciphertext)
}

func writeSnapshotBytes(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".plumtree-state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
