package buildassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	bundle, err := Extract()
	if err != nil {
		t.Fatal(err)
	}
	root := bundle.root
	t.Cleanup(func() { _ = bundle.Cleanup() })

	for _, module := range bundle.WorkspaceModules {
		if _, err := os.Stat(filepath.Join(module, "go.mod")); err != nil {
			t.Errorf("module %q: %v", module, err)
		}
	}
	if !strings.HasPrefix(bundle.GoProxy, "file://") {
		t.Fatalf("GoProxy = %q, want file URL", bundle.GoProxy)
	}
	if err := bundle.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("bundle root still exists after cleanup: %v", err)
	}
}

func TestExtractArchiveRejectsTraversal(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(zw)
	payload := []byte("unsafe")
	if err := tw.WriteHeader(&tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	err := extractArchive(compressed.Bytes(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("extractArchive error = %v, want unsafe path", err)
	}
}
