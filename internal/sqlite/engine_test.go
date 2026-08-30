package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenPlaintextDevelopmentMode(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE records (value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO records(value) VALUES ('ok')`); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRow(`SELECT value FROM records`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Fatalf("value = %q", got)
	}

	info, err := db.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Encrypted {
		t.Fatal("plaintext database reported as encrypted")
	}
	if info.JournalMode != "wal" {
		t.Fatalf("journal mode = %q, want wal", info.JournalMode)
	}
}

func TestOpenCreatesPrivateDatabaseFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database permissions = %o, want 600", got)
	}
}

func TestKeyPolicyRejectsUnsafeConfigurations(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "state.db"), []byte("short")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("short key error = %v", err)
	}
	if _, err := OpenWithConfig(Config{Path: "file:caller-controlled?mode=memory"}); err == nil {
		t.Fatal("DSN was accepted as a path")
	}
	_, err := Open(filepath.Join(t.TempDir(), "state.db"), make([]byte, KeySize))
	if compiledSQLCipher {
		if err != nil {
			t.Fatalf("keyed SQLCipher build error = %v", err)
		}
	} else if !errors.Is(err, ErrSQLCipherUnavailable) {
		t.Fatalf("keyed development build error = %v", err)
	}
	if got := fmt.Sprintf("%v", Config{Key: make([]byte, KeySize)}); got != "sqlite.Config{redacted}" {
		t.Fatalf("config formatting leaked details: %s", got)
	}
}

func TestSQLCipherContract(t *testing.T) {
	if !compiledSQLCipher {
		t.Skip("SQLCipher qualification requires the sqlcipher build tag and target-native library")
	}

	key := []byte("01234567890123456789012345678901")
	path := filepath.Join(t.TempDir(), "encrypted.db")
	db, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE records (value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO records(value) VALUES ('secret-value')`); err != nil {
		t.Fatal(err)
	}
	info, err := db.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.CipherVersion == "" || info.JournalMode != "wal" {
		t.Fatalf("engine info = %+v", info)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, key)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	var value string
	if err := reopened.QueryRow(`SELECT value FROM records`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "secret-value" {
		t.Fatalf("value = %q", value)
	}
	if _, err := Open(path, []byte("abcdefghijklmnopqrstuvwxyz123456")); !errors.Is(err, ErrKeyRejected) {
		t.Fatalf("wrong key error = %v", err)
	}
}

func TestBackupKeepsModeBoundary(t *testing.T) {
	key := []byte(nil)
	if compiledSQLCipher {
		key = []byte("01234567890123456789012345678901")
	}
	dir := t.TempDir()
	plain, err := Open(filepath.Join(dir, "plain.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if _, err := plain.Exec(`CREATE TABLE records (value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Exec(`INSERT INTO records(value) VALUES ('backup-value')`); err != nil {
		t.Fatal(err)
	}
	var sourceValue string
	if err := plain.QueryRow(`SELECT value FROM records`).Scan(&sourceValue); err != nil {
		t.Fatal(err)
	}
	destination, err := Open(filepath.Join(dir, "backup.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if err := Backup(context.Background(), destination, plain); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := destination.QueryRow(`SELECT value FROM records`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "backup-value" {
		t.Fatalf("backup value = %q", value)
	}
	if err := Backup(context.Background(), plain, nil); err == nil {
		t.Fatal("nil-source backup unexpectedly succeeded")
	}
}
