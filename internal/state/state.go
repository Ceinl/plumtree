// Package state owns the current-format offline state bundle. It deliberately
// has no legacy discovery, import, export, or migration readers.
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/internal/sqlite"
	"golang.org/x/crypto/ssh"
)

const formatVersion = 1

var (
	ErrInvalid       = errors.New("state: invalid input")
	ErrUnsafePath    = errors.New("state: unsafe path")
	ErrCorrupt       = errors.New("state: corrupt bundle")
	ErrInsufficient  = errors.New("state: insufficient space")
	ErrJournalExists = errors.New("state: restore journal already exists")
)

// Paths selects exactly the three current state components. Empty paths are
// rejected; this avoids silently omitting an orphan KV store or host identity.
type Paths struct {
	Database    string
	KVRoot      string
	SSHIdentity string
}

// Options controls interruption testing and the clock used in manifests.
type Options struct {
	Now            func() time.Time
	Interrupt      func(phase string) error
	AvailableBytes func(path string) (uint64, error)
}

type DatabaseInventory struct {
	Path      string `json:"path"`
	Encrypted bool   `json:"encrypted"`
	KeyID     string `json:"keyId"`
}

type KVInventory struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

type SSHInventory struct {
	Path        string `json:"path"`
	Fingerprint string `json:"fingerprint"`
	KeyType     string `json:"keyType"`
}

// Inventory never includes database keys, KV values, or SSH private bytes.
type InventoryReport struct {
	Version  int               `json:"version"`
	Database DatabaseInventory `json:"database"`
	KV       KVInventory       `json:"kv"`
	SSH      SSHInventory      `json:"ssh"`
}

type fileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Version        int         `json:"version"`
	CreatedAt      time.Time   `json:"createdAt"`
	Database       fileEntry   `json:"database"`
	DatabaseKeyID  string      `json:"databaseKeyId"`
	KV             []fileEntry `json:"kv"`
	SSH            fileEntry   `json:"ssh"`
	SSHFingerprint string      `json:"sshFingerprint"`
	SSHKeyType     string      `json:"sshKeyType"`
}

type journal struct {
	Version    int         `json:"version"`
	CreatedAt  time.Time   `json:"createdAt"`
	Operations []journalOp `json:"operations"`
}

type journalOp struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	Staged string `json:"staged"`
	Backup string `json:"backup"`
	Done   bool   `json:"done"`
}

func defaultOptions(options Options) Options {
	if options.Now == nil {
		options.Now = time.Now
	}
	return options
}

func validatePaths(paths Paths) error {
	for name, value := range map[string]string{"database": paths.Database, "kv root": paths.KVRoot, "ssh identity": paths.SSHIdentity} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s path is required", ErrInvalid, name)
		}
		if filepath.Clean(value) == "." {
			return fmt.Errorf("%w: %s path", ErrInvalid, name)
		}
	}
	return nil
}

func validateBundlePath(path string) error {
	if strings.TrimSpace(path) == "" || filepath.Clean(path) == "." {
		return fmt.Errorf("%w: bundle path", ErrInvalid)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: bundle is a symlink", ErrUnsafePath)
	}
	return nil
}

func privateRegular(path string, allowMissing bool) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnsafePath, path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrInvalid, path)
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("%w: %s has insecure permissions", ErrUnsafePath, path)
	}
	return info, nil
}

func privateDir(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrUnsafePath, path)
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("%w: %s has insecure permissions", ErrUnsafePath, path)
	}
	return info, nil
}

func hashFile(path string) (fileEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileEntry{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return fileEntry{}, err
	}
	return fileEntry{Path: path, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err = out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func readManifest(bundle string) (manifest, error) {
	var m manifest
	info, err := privateRegular(filepath.Join(bundle, "manifest.json"), false)
	if err != nil {
		return m, err
	}
	if info.Size() > 1<<20 {
		return m, fmt.Errorf("%w: manifest too large", ErrCorrupt)
	}
	b, err := os.ReadFile(filepath.Join(bundle, "manifest.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%w: manifest: %v", ErrCorrupt, err)
	}
	if m.Version != formatVersion || len(m.KV) > 100000 {
		return m, fmt.Errorf("%w: unsupported manifest version", ErrCorrupt)
	}
	return m, nil
}

func verifyEntry(root string, entry fileEntry) error {
	if entry.Path == "" || filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path || strings.HasPrefix(entry.Path, ".."+string(filepath.Separator)) || entry.Path == ".." {
		return fmt.Errorf("%w: bundle entry", ErrCorrupt)
	}
	path := filepath.Join(root, entry.Path)
	info, err := privateRegular(path, false)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrCorrupt, entry.Path, err)
	}
	if info.Size() != entry.Size {
		return fmt.Errorf("%w: size mismatch for %s", ErrCorrupt, entry.Path)
	}
	got, err := hashFile(path)
	if err != nil {
		return err
	}
	if got.SHA256 != entry.SHA256 {
		return fmt.Errorf("%w: digest mismatch for %s", ErrCorrupt, entry.Path)
	}
	return nil
}

func collectKV(root string) ([]fileEntry, error) {
	if _, err := privateDir(root); err != nil {
		return nil, err
	}
	var entries []fileEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: KV symlink %s", ErrUnsafePath, path)
		}
		if d.IsDir() {
			info, e := d.Info()
			if e != nil {
				return e
			}
			if info.Mode().Perm()&0077 != 0 {
				return fmt.Errorf("%w: KV directory %s has insecure permissions", ErrUnsafePath, path)
			}
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("%w: KV file %s", ErrUnsafePath, path)
		}
		entry, hashErr := hashFile(path)
		if hashErr != nil {
			return hashErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		entry.Path = filepath.ToSlash(rel)
		entries = append(entries, entry)
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, err
}

func identityInfo(path string) (string, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return "", "", fmt.Errorf("%w: invalid SSH identity", ErrCorrupt)
	}
	return ssh.FingerprintSHA256(signer.PublicKey()), signer.PublicKey().Type(), nil
}

// Inventory returns non-secret identifiers and component counts for current
// state. It never reads key bytes into the result.
func Inventory(ctx context.Context, paths Paths, key []byte) (InventoryReport, error) {
	if err := validatePaths(paths); err != nil {
		return InventoryReport{}, err
	}
	if _, err := privateRegular(paths.Database, false); err != nil {
		return InventoryReport{}, err
	}
	if _, err := privateDir(paths.KVRoot); err != nil {
		return InventoryReport{}, err
	}
	if _, err := privateRegular(paths.SSHIdentity, false); err != nil {
		return InventoryReport{}, err
	}
	db, err := sqlite.Open(paths.Database, key)
	if err != nil {
		return InventoryReport{}, err
	}
	defer db.Close()
	info, err := db.Info(ctx)
	if err != nil {
		return InventoryReport{}, err
	}
	entries, err := collectKV(paths.KVRoot)
	if err != nil {
		return InventoryReport{}, err
	}
	fingerprint, keyType, err := identityInfo(paths.SSHIdentity)
	if err != nil {
		return InventoryReport{}, err
	}
	var bytes int64
	for _, e := range entries {
		bytes += e.Size
	}
	return InventoryReport{Version: formatVersion, Database: DatabaseInventory{Path: paths.Database, Encrypted: info.Encrypted, KeyID: keyID(key)}, KV: KVInventory{Files: len(entries), Bytes: bytes}, SSH: SSHInventory{Path: paths.SSHIdentity, Fingerprint: fingerprint, KeyType: keyType}}, nil
}

func keyID(key []byte) string {
	if len(key) == 0 {
		return "plaintext"
	}
	sum := sha256.Sum256(key)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Preflight estimates bytes needed for a bundle and checks the supplied space
// provider before staging. A provider is injectable for deterministic tests.
func Preflight(destination string, bytesNeeded int64, options Options) error {
	if bytesNeeded < 0 {
		return fmt.Errorf("%w: negative size", ErrInvalid)
	}
	if options.AvailableBytes == nil {
		return nil
	}
	free, err := options.AvailableBytes(destination)
	if err != nil {
		return fmt.Errorf("state: space check: %w", err)
	}
	if uint64(bytesNeeded) > free {
		return fmt.Errorf("%w: need %d bytes, have %d", ErrInsufficient, bytesNeeded, free)
	}
	return nil
}

// Backup creates a self-contained current-format bundle and atomically
// publishes it at destination. Existing destination bundles are retained as a
// sibling .previous directory rather than deleted.
func Backup(ctx context.Context, paths Paths, destination string, key []byte, options Options) error {
	options = defaultOptions(options)
	if err := validatePaths(paths); err != nil {
		return err
	}
	if err := validateBundlePath(destination); err != nil {
		return err
	}
	if _, err := privateRegular(paths.Database, false); err != nil {
		return err
	}
	if _, err := privateDir(paths.KVRoot); err != nil {
		return err
	}
	if _, err := privateRegular(paths.SSHIdentity, false); err != nil {
		return err
	}
	entries, err := collectKV(paths.KVRoot)
	if err != nil {
		return err
	}
	sshFP, sshType, err := identityInfo(paths.SSHIdentity)
	if err != nil {
		return err
	}
	dbInfo, err := os.Stat(paths.Database)
	if err != nil {
		return err
	}
	var need int64 = dbInfo.Size()
	for _, e := range entries {
		need += e.Size
	}
	if err := Preflight(destination, need, options); err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".plumtree-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := interrupt(options, "backup-staging"); err != nil {
		return err
	}
	if err := copyDatabase(ctx, paths.Database, filepath.Join(stage, "database.db"), key); err != nil {
		return err
	}
	if err := copyFile(paths.SSHIdentity, filepath.Join(stage, "ssh-identity"), 0600); err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyFile(filepath.Join(paths.KVRoot, filepath.FromSlash(e.Path)), filepath.Join(stage, "kv", filepath.FromSlash(e.Path)), 0600); err != nil {
			return err
		}
	}
	dbEntry, err := hashFile(filepath.Join(stage, "database.db"))
	if err != nil {
		return err
	}
	dbEntry.Path = "database.db"
	sshEntry, err := hashFile(filepath.Join(stage, "ssh-identity"))
	if err != nil {
		return err
	}
	sshEntry.Path = "ssh-identity"
	for i := range entries {
		entries[i].Path = filepath.ToSlash(filepath.Join("kv", filepath.FromSlash(entries[i].Path)))
	}
	m := manifest{Version: formatVersion, CreatedAt: options.Now(), Database: dbEntry, DatabaseKeyID: keyID(key), KV: entries, SSH: sshEntry, SSHFingerprint: sshFP, SSHKeyType: sshType}
	if err := writeJSON(filepath.Join(stage, "manifest.json"), m); err != nil {
		return err
	}
	if err := verifyBundle(stage, m, key); err != nil {
		return err
	}
	if err := interrupt(options, "backup-verified"); err != nil {
		return err
	}
	if err := publishDirectory(stage, destination); err != nil {
		return err
	}
	if err := interrupt(options, "backup-committed"); err != nil {
		return err
	}
	return nil
}

func copyDatabase(ctx context.Context, source, destination string, key []byte) error {
	src, err := sqlite.Open(source, key)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := sqlite.Verify(ctx, src); err != nil {
		return err
	}
	dst, err := sqlite.Open(destination, key)
	if err != nil {
		return err
	}
	if err := sqlite.Backup(ctx, dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Chmod(destination, 0600)
}
func interrupt(options Options, phase string) error {
	if options.Interrupt == nil {
		return nil
	}
	if err := options.Interrupt(phase); err != nil {
		return fmt.Errorf("state: interrupted at %s: %w", phase, err)
	}
	return nil
}

func publishDirectory(stage, destination string) error {
	if err := validateBundlePath(destination); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		return os.Rename(stage, destination)
	} else if err != nil {
		return err
	}
	previous := destination + ".previous"
	if _, err := os.Lstat(previous); err == nil {
		return fmt.Errorf("%w: backup predecessor exists", ErrInvalid)
	}
	if err := os.Rename(destination, previous); err != nil {
		return err
	}
	if err := os.Rename(stage, destination); err != nil {
		_ = os.Rename(previous, destination)
		return err
	}
	return nil
}

func verifyBundle(bundle string, m manifest, key []byte) error {
	if err := verifyEntry(bundle, m.Database); err != nil {
		return err
	}
	if err := verifyEntry(bundle, m.SSH); err != nil {
		return err
	}
	for _, e := range m.KV {
		if err := verifyEntry(bundle, e); err != nil {
			return err
		}
	}
	if _, _, err := identityInfo(filepath.Join(bundle, m.SSH.Path)); err != nil {
		return err
	}
	db, err := sqlite.Open(filepath.Join(bundle, m.Database.Path), key)
	if err != nil {
		return err
	}
	defer db.Close()
	return sqlite.Verify(context.Background(), db)
}

// Restore verifies the complete bundle before any live component is moved.
// If interruption is requested, the journal and staged files remain for
// Recover to roll forward without guessing which component was replaced.
func Restore(ctx context.Context, bundle string, paths Paths, key []byte, options Options) error {
	options = defaultOptions(options)
	if err := validateBundlePath(bundle); err != nil {
		return err
	}
	if _, err := privateDir(bundle); err != nil {
		return err
	}
	if err := validatePaths(paths); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(bundle, "manifest.json")); err != nil {
		return err
	}
	m, err := readManifest(bundle)
	if err != nil {
		return err
	}
	if err := verifyBundle(bundle, m, key); err != nil {
		return err
	}
	if m.DatabaseKeyID != keyID(key) {
		return fmt.Errorf("%w: database key identifier mismatch", ErrCorrupt)
	}
	if fp, typ, err := identityInfo(filepath.Join(bundle, m.SSH.Path)); err != nil || fp != m.SSHFingerprint || typ != m.SSHKeyType {
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: SSH identity fingerprint mismatch", ErrCorrupt)
	}
	journalPath := filepath.Join(filepath.Dir(paths.Database), ".plumtree-restore-journal.json")
	if _, err := os.Lstat(journalPath); err == nil {
		return ErrJournalExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(paths.Database), ".plumtree-restore-")
	if err != nil {
		return err
	}
	keep := true
	defer func() {
		if !keep {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := copyFile(filepath.Join(bundle, m.Database.Path), filepath.Join(stage, "database.db"), 0600); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(bundle, m.SSH.Path), filepath.Join(stage, "ssh-identity"), 0600); err != nil {
		return err
	}
	for _, e := range m.KV {
		rel := strings.TrimPrefix(e.Path, "kv"+string(filepath.Separator))
		if err := copyFile(filepath.Join(bundle, e.Path), filepath.Join(stage, "kv", filepath.FromSlash(rel)), 0600); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(stage, "kv"), 0700); err != nil {
		return err
	}
	ops := []journalOp{{Name: "database", Target: paths.Database, Staged: filepath.Join(stage, "database.db"), Backup: paths.Database + ".restore-previous"}, {Name: "kv", Target: paths.KVRoot, Staged: filepath.Join(stage, "kv"), Backup: paths.KVRoot + ".restore-previous"}, {Name: "ssh-identity", Target: paths.SSHIdentity, Staged: filepath.Join(stage, "ssh-identity"), Backup: paths.SSHIdentity + ".restore-previous"}}
	j := journal{Version: formatVersion, CreatedAt: options.Now(), Operations: ops}
	if err := writeJSON(journalPath, j); err != nil {
		return err
	}
	if err := interrupt(options, "restore-journal-written"); err != nil {
		return err
	}
	if err := applyJournal(journalPath, &j); err != nil {
		return err
	}
	keep = false
	return nil
}

func applyJournal(path string, j *journal) error {
	for i := range j.Operations {
		op := &j.Operations[i]
		if op.Done {
			continue
		}
		if err := replaceOperation(op); err != nil {
			return err
		}
		op.Done = true
		if err := writeJournal(path, *j); err != nil {
			return err
		}
	}
	return os.Remove(path)
}
func writeJournal(path string, j journal) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := writeJSON(tmp, j); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func replaceOperation(op *journalOp) error {
	stageExists := false
	if _, err := os.Lstat(op.Staged); err == nil {
		stageExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	targetExists := false
	if info, err := os.Lstat(op.Target); err == nil {
		targetExists = true
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: restore target %s", ErrUnsafePath, op.Target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !stageExists {
		if targetExists {
			return nil
		}
		return fmt.Errorf("%w: missing staged %s", ErrCorrupt, op.Name)
	}
	if targetExists {
		if _, err := os.Lstat(op.Backup); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(op.Target, op.Backup); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	if runtime.GOOS == "windows" && targetExists {
		_ = os.Remove(op.Target)
	}
	return os.Rename(op.Staged, op.Target)
}

// Recover completes an interrupted restore using only the local commit-intent
// journal. It never reads legacy state or invents missing components.
func Recover(paths Paths) error {
	if err := validatePaths(paths); err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(paths.Database), ".plumtree-restore-journal.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var j journal
	if err := json.Unmarshal(b, &j); err != nil {
		return fmt.Errorf("%w: restore journal", ErrCorrupt)
	}
	if j.Version != formatVersion {
		return fmt.Errorf("%w: restore journal version", ErrCorrupt)
	}
	return applyJournal(path, &j)
}

// RekeyDatabase changes an already encrypted database key in place. Plaintext
// state remains plaintext when newKey is empty; plaintext-to-encrypted and
// SQLCipher operations are rejected by the engine when unavailable.
func RekeyDatabase(ctx context.Context, path string, oldKey, newKey []byte) error {
	if _, err := privateRegular(path, false); err != nil {
		return err
	}
	db, err := sqlite.Open(path, oldKey)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := sqlite.Rekey(ctx, db, newKey); err != nil {
		return err
	}
	return sqlite.Verify(ctx, db)
}
