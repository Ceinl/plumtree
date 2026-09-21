// Package paired contains the clean private client used to manage paired
// Plumtree servers. It is deliberately separate from the legacy project CLI
// configuration while the clean command surface is staged.
package paired

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Ceinl/plumtree/internal/transport"
)

const (
	StoreVersion         = 1
	DefaultConfigDir     = "plumtree"
	DefaultStoreName     = "servers.json"
	defaultDirectoryMode = 0700
	privateFileMode      = 0600
	maxStoreBytes        = 1 << 20
)

var (
	ErrInvalidStore       = errors.New("paired: invalid server store")
	ErrServerNotFound     = errors.New("paired: server not found")
	ErrDuplicateServer    = errors.New("paired: duplicate server")
	ErrCurrentServer      = errors.New("paired: no current server")
	ErrInvalidTarget      = errors.New("paired: invalid target")
	ErrLastDevice         = errors.New("paired: refusing to remove the last device")
	ErrRemoteRevokeFailed = errors.New("paired: remote revocation failed")
)

// ServerRecord contains only the identity and references needed to reconnect
// to one server. In particular, it never contains a bearer token, cookie,
// pairing phrase, or private key bytes.
type ServerRecord struct {
	Name               string `json:"name"`
	ServerID           string `json:"serverID"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	HostKeyAlgorithm   string `json:"hostKeyAlgorithm"`
	HostKeyFingerprint string `json:"hostKeyFingerprint"`
	HostKeyPublicKey   string `json:"hostKeyPublicKey,omitempty"`
	ProductVersion     string `json:"productVersion"`
	AuthorID           string `json:"authorID,omitempty"`
	AuthorHandle       string `json:"authorHandle,omitempty"`
	DeviceID           string `json:"deviceID,omitempty"`
	DeviceName         string `json:"deviceName,omitempty"`
	KeyRef             string `json:"keyRef"`
}

func (r ServerRecord) Endpoint() transport.Endpoint {
	return transport.Endpoint{Host: r.Host, Port: r.Port}
}

func (r ServerRecord) validate() error {
	if !validName(r.Name) || strings.TrimSpace(r.ServerID) == "" ||
		strings.TrimSpace(r.Host) == "" || strings.ContainsAny(r.Host, "/\\") ||
		r.Port < 1 || r.Port > 65535 || strings.TrimSpace(r.HostKeyAlgorithm) == "" ||
		strings.TrimSpace(r.HostKeyFingerprint) == "" || strings.TrimSpace(r.ProductVersion) == "" ||
		strings.TrimSpace(r.KeyRef) == "" {
		return fmt.Errorf("%w: incomplete server record", ErrInvalidStore)
	}
	if filepath.IsAbs(r.KeyRef) || filepath.Clean(r.KeyRef) != r.KeyRef || strings.HasPrefix(r.KeyRef, "../") || r.KeyRef == ".." {
		return fmt.Errorf("%w: key reference must be relative", ErrInvalidStore)
	}
	return nil
}

func validName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if name != trimmed || name == "" || len(name) > 64 || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, "/\\\x00\t\r\n")
}

type Store struct {
	Version int            `json:"version"`
	Current string         `json:"current,omitempty"`
	Servers []ServerRecord `json:"servers"`
	mu      sync.RWMutex
}

func NewStore() *Store { return &Store{Version: StoreVersion, Servers: []ServerRecord{}} }

func (s *Store) validate() error {
	if s == nil || s.Version != StoreVersion {
		return fmt.Errorf("%w: unsupported format", ErrInvalidStore)
	}
	seen := make(map[string]struct{}, len(s.Servers))
	for _, server := range s.Servers {
		if err := server.validate(); err != nil {
			return err
		}
		if _, ok := seen[server.Name]; ok {
			return fmt.Errorf("%w: %q", ErrDuplicateServer, server.Name)
		}
		seen[server.Name] = struct{}{}
	}
	if s.Current != "" {
		if _, ok := seen[s.Current]; !ok {
			return fmt.Errorf("%w: current %q", ErrServerNotFound, s.Current)
		}
	}
	return nil
}

func cloneStore(s *Store) *Store {
	out := &Store{Version: s.Version, Current: s.Current, Servers: append([]ServerRecord(nil), s.Servers...)}
	return out
}

// Load reads a store only from a regular 0600 file. Symlinks and permissive
// files are rejected before any JSON is parsed.
func Load(path string) (*Store, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewStore(), nil
		}
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != privateFileMode {
		return nil, fmt.Errorf("%w: store must be a regular 0600 file", ErrInvalidStore)
	}
	if info.Size() > maxStoreBytes {
		return nil, fmt.Errorf("%w: store is too large", ErrInvalidStore)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var store Store
	dec := json.NewDecoder(io.LimitReader(f, maxStoreBytes+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&store); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidStore, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("%w: trailing JSON", ErrInvalidStore)
		}
		return nil, fmt.Errorf("%w: trailing data: %v", ErrInvalidStore, err)
	}
	if err := store.validate(); err != nil {
		return nil, err
	}
	return &store, nil
}

// Save atomically replaces path, creating a private 0700 parent directory.
func Save(path string, store *Store) error {
	if store == nil {
		return fmt.Errorf("%w: nil store", ErrInvalidStore)
	}
	store.mu.RLock()
	copy := cloneStore(store)
	store.mu.RUnlock()
	if err := copy.validate(); err != nil {
		return err
	}
	if filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) || strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: invalid path", ErrInvalidStore)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, defaultDirectoryMode); err != nil {
		return err
	}
	if err := os.Chmod(dir, defaultDirectoryMode); err != nil {
		return err
	}
	data, err := json.MarshalIndent(copy, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".servers-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(privateFileMode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

func DefaultPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("PLUMTREE_PT_SERVERS_FILE")); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DefaultConfigDir, DefaultStoreName), nil
}

func (s *Store) Add(record ServerRecord) error {
	if err := record.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.Servers {
		if old.Name == record.Name || old.ServerID == record.ServerID {
			return fmt.Errorf("%w: %q", ErrDuplicateServer, record.Name)
		}
	}
	s.Servers = append(s.Servers, record)
	if s.Current == "" {
		s.Current = record.Name
	}
	return nil
}

func (s *Store) Get(name string) (ServerRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.Servers {
		if record.Name == name {
			return record, nil
		}
	}
	return ServerRecord{}, fmt.Errorf("%w: %q", ErrServerNotFound, name)
}

func (s *Store) CurrentRecord() (ServerRecord, error) {
	s.mu.RLock()
	current := s.Current
	s.mu.RUnlock()
	if current == "" {
		return ServerRecord{}, ErrCurrentServer
	}
	return s.Get(current)
}

func (s *Store) Select(name string) error {
	if _, err := s.Get(name); err != nil {
		return err
	}
	s.mu.Lock()
	s.Current = name
	s.mu.Unlock()
	return nil
}

func (s *Store) Rename(oldName, newName string) error {
	if !validName(newName) {
		return fmt.Errorf("%w: invalid server name", ErrInvalidTarget)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.Servers {
		if record.Name == newName {
			return fmt.Errorf("%w: %q", ErrDuplicateServer, newName)
		}
	}
	for i := range s.Servers {
		if s.Servers[i].Name == oldName {
			s.Servers[i].Name = newName
			if s.Current == oldName {
				s.Current = newName
			}
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrServerNotFound, oldName)
}

func (s *Store) Remove(name string) (ServerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, record := range s.Servers {
		if record.Name != name {
			continue
		}
		s.Servers = append(s.Servers[:i], s.Servers[i+1:]...)
		if s.Current == name {
			s.Current = ""
			if len(s.Servers) > 0 {
				s.Current = s.Servers[0].Name
			}
		}
		return record, nil
	}
	return ServerRecord{}, fmt.Errorf("%w: %q", ErrServerNotFound, name)
}

// Target is the explicitly selected destination shown to callers before a
// network operation is made. An override never changes the persisted record.
type Target struct {
	Name     string
	Endpoint transport.Endpoint
	ServerID string
}

func (s *Store) ResolveTarget(name string, override *transport.Endpoint) (Target, error) {
	if name == "" {
		record, err := s.CurrentRecord()
		if err != nil {
			return Target{}, err
		}
		name = record.Name
	}
	record, err := s.Get(name)
	if err != nil {
		return Target{}, err
	}
	target := Target{Name: record.Name, Endpoint: record.Endpoint(), ServerID: record.ServerID}
	if override != nil {
		if override.Host == "" || override.Port < 1 || override.Port > 65535 {
			return Target{}, fmt.Errorf("%w: endpoint override", ErrInvalidTarget)
		}
		target.Endpoint = *override
	}
	return target, nil
}

// Redacted returns data suitable for diagnostics. KeyRef is intentionally
// omitted because even a local path can reveal operator layout.
type RedactedRecord struct {
	Name               string `json:"name"`
	ServerID           string `json:"serverID"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	HostKeyAlgorithm   string `json:"hostKeyAlgorithm"`
	HostKeyFingerprint string `json:"hostKeyFingerprint"`
	ProductVersion     string `json:"productVersion"`
	AuthorID           string `json:"authorID,omitempty"`
	AuthorHandle       string `json:"authorHandle,omitempty"`
	DeviceID           string `json:"deviceID,omitempty"`
	DeviceName         string `json:"deviceName,omitempty"`
}

func (r ServerRecord) Redacted() RedactedRecord {
	return RedactedRecord{Name: r.Name, ServerID: r.ServerID, Host: r.Host, Port: r.Port,
		HostKeyAlgorithm: r.HostKeyAlgorithm, HostKeyFingerprint: r.HostKeyFingerprint,
		ProductVersion: r.ProductVersion, AuthorID: r.AuthorID, AuthorHandle: r.AuthorHandle,
		DeviceID: r.DeviceID, DeviceName: r.DeviceName}
}

// Unpair revokes the device before deleting its local record. forget=true is
// the explicit recovery path for a server that cannot be reached.
func Unpair(ctx context.Context, store *Store, name string, forget bool, revoke func(context.Context, ServerRecord) error) error {
	if store == nil {
		return fmt.Errorf("%w: nil store", ErrInvalidStore)
	}
	record, err := store.Get(name)
	if err != nil {
		return err
	}
	if !forget {
		if revoke == nil {
			return fmt.Errorf("%w: no revocation transport", ErrRemoteRevokeFailed)
		}
		if err := revoke(ctx, record); err != nil {
			return fmt.Errorf("%w: %v", ErrRemoteRevokeFailed, err)
		}
	}
	_, err = store.Remove(name)
	return err
}
