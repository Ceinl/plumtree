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
	if migrated {
		if err := writeSnapshotFile(path, loaded.snapshotLocked()); err != nil {
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
	return nil
}

func (s *Store) persistLocked() error {
	if s.persistPath == "" {
		return nil
	}
	return writeSnapshotFile(s.persistPath, s.snapshotLocked())
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
