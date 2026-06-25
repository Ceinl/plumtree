package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	buildworker "github.com/Ceinl/plumtree/build-worker"
)

func TestSourceDigestChangesWithAppFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plumtree.json"), []byte(`{"name":"counter","type":"tui"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "app", "main.go")
	if err := os.WriteFile(app, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := packedDigest(t, dir)
	if err := os.WriteFile(app, []byte("package main\nfunc main(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := packedDigest(t, dir)
	if first == second {
		t.Fatal("source digest did not change")
	}
	if !strings.HasPrefix(first, "sha256:") || len(first) != len("sha256:")+64 {
		t.Fatalf("bad digest: %q", first)
	}
}

func packedDigest(t *testing.T, dir string) string {
	t.Helper()
	archive, err := buildworker.PackSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	return buildworker.SourceDigest(archive)
}
