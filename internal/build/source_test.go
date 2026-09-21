package build

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPackSourceSkipsNestedSymlinks(t *testing.T) {
	project := t.TempDir()
	appDir := filepath.Join(project, "app")
	if err := os.Mkdir(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(target, []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(appDir, "linked.go")); err != nil {
		t.Skipf("create source symlink: %v", err)
	}

	archive, err := PackSource(project)
	if err != nil {
		t.Fatalf("pack source: %v", err)
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	if len(names) != 1 || names[0] != "app/main.go" {
		t.Fatalf("archive entries = %v, want only app/main.go", names)
	}
}
