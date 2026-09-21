package state

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/internal/sqlite"
)

func TestBackupRestoreInventoryAndOrphanKV(t *testing.T) {
	paths := makeState(t)
	ctx := context.Background()
	db, err := sqlite.Open(paths.Database, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE values_store(value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO values_store(value) VALUES ('before-backup')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.Database, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.KVRoot, "orphan"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.KVRoot, "orphan", "value.json"), []byte(`{"n":1}`), 0600); err != nil {
		t.Fatal(err)
	}

	bundle := filepath.Join(t.TempDir(), "state.bundle")
	if err := Backup(ctx, paths, bundle, nil, Options{}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	inv, err := Inventory(ctx, paths, nil)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if inv.Database.KeyID != "plaintext" || inv.KV.Files != 1 || inv.SSH.Fingerprint == "" {
		t.Fatalf("inventory: %+v", inv)
	}

	db, err = sqlite.Open(paths.Database, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE values_store SET value='changed'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.KVRoot, "orphan", "value.json"), []byte(`{"n":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, bundle, paths, nil, Options{}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	db, err = sqlite.Open(paths.Database, nil)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM values_store`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "before-backup" {
		t.Fatalf("restored database value = %q", value)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(paths.KVRoot, "orphan", "value.json"))
	if err != nil || string(got) != `{"n":1}` {
		t.Fatalf("restored orphan KV = %q, %v", got, err)
	}
}

func TestRestoreJournalRollsForwardAndProtectsLiveState(t *testing.T) {
	paths := makeState(t)
	db, err := sqlite.Open(paths.Database, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE values_store(value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO values_store(value) VALUES ('old')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(paths.Database, 0600)
	bundle := filepath.Join(t.TempDir(), "state.bundle")
	if err := Backup(context.Background(), paths, bundle, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(paths.Database, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE values_store SET value='new'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(paths.Database, 0600)

	interruptErr := errors.New("simulated power loss")
	err = Restore(context.Background(), bundle, paths, nil, Options{Interrupt: func(phase string) error {
		if phase == "restore-journal-written" {
			return interruptErr
		}
		return nil
	}})
	if !errors.Is(err, interruptErr) {
		t.Fatalf("interrupted restore = %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(paths.Database), ".plumtree-restore-journal.json")); err != nil {
		t.Fatalf("journal missing: %v", err)
	}
	if err := Recover(paths); err != nil {
		t.Fatalf("recover: %v", err)
	}

	db, err = sqlite.Open(paths.Database, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT value FROM values_store`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "old" {
		t.Fatalf("roll-forward value = %q", value)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(paths.Database), ".plumtree-restore-journal.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains: %v", err)
	}
}

func TestStateRejectsSymlinksAndRedactsKeys(t *testing.T) {
	paths := makeState(t)
	if err := os.Symlink(paths.KVRoot, paths.KVRoot+"-link"); err != nil {
		t.Fatal(err)
	}
	bad := paths
	bad.KVRoot += "-link"
	if _, err := Inventory(context.Background(), bad, nil); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink inventory = %v", err)
	}
	if err := Preflight(t.TempDir(), 100, Options{AvailableBytes: func(string) (uint64, error) { return 99, nil }}); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("space preflight = %v", err)
	}
	report, err := InventoryReport{Version: 1, Database: DatabaseInventory{KeyID: "sha256:identifier-only"}}.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(report), "01234567890123456789012345678901") {
		t.Fatal("inventory contains key bytes")
	}
}

func (i InventoryReport) MarshalJSON() ([]byte, error) {
	type alias InventoryReport
	return json.Marshal(alias(i))
}

func makeState(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	paths := Paths{Database: filepath.Join(root, "state.db"), KVRoot: filepath.Join(root, "kv"), SSHIdentity: filepath.Join(root, "host-key")}
	if err := os.Mkdir(paths.KVRoot, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(paths.Database, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.Database, 0600); err != nil {
		t.Fatal(err)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SSHIdentity, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return paths
}
