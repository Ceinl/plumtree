// Package buildassets provides the SDK and TUI runtime bundled into the
// standalone control-plane binary for hermetic in-process builds.
package buildassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxBundleFiles = 10_000
	maxBundleBytes = 128 << 20
)

//go:generate go run ./cmd/generate -repo ../../.. -out bundle.tar.gz

//go:embed bundle.tar.gz
var bundleArchive []byte

// Bundle is a materialized copy of the embedded build dependencies. Call
// Cleanup after the build backend has stopped using WorkspaceModules.
type Bundle struct {
	WorkspaceModules []string
	GoProxy          string
	root             string
}

// Extract materializes the embedded modules and offline module proxy into a
// private temporary directory. Go requires workspace modules to exist on disk,
// so the archive cannot be consumed directly from embed.FS.
func Extract() (*Bundle, error) {
	root, err := os.MkdirTemp("", "plumtree-build-assets-*")
	if err != nil {
		return nil, fmt.Errorf("create build assets directory: %w", err)
	}
	if err := extractArchive(bundleArchive, root); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}

	modules := []string{
		filepath.Join(root, "sdk"),
		filepath.Join(root, "tui-runtime"),
	}
	for _, module := range modules {
		if info, err := os.Stat(filepath.Join(module, "go.mod")); err != nil || info.IsDir() {
			_ = os.RemoveAll(root)
			return nil, fmt.Errorf("embedded build module %q is incomplete", filepath.Base(module))
		}
	}

	proxyDir := filepath.Join(root, "modproxy", "cache", "download")
	proxyURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(proxyDir)}).String()
	return &Bundle{WorkspaceModules: modules, GoProxy: proxyURL, root: root}, nil
}

// Cleanup removes the materialized build dependencies.
func (b *Bundle) Cleanup() error {
	if b == nil || b.root == "" {
		return nil
	}
	err := os.RemoveAll(b.root)
	b.root = ""
	return err
}

func extractArchive(compressed []byte, root string) error {
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("open embedded build assets: %w", err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	var files int
	var total int64
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read embedded build assets: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf("embedded build asset %q is not a regular file", header.Name)
		}
		files++
		total += header.Size
		if files > maxBundleFiles || total > maxBundleBytes {
			return fmt.Errorf("embedded build assets exceed extraction limits")
		}

		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("embedded build asset has unsafe path %q", header.Name)
		}
		target := filepath.Join(root, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create embedded build asset directory: %w", err)
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
		if err != nil {
			return fmt.Errorf("create embedded build asset %q: %w", header.Name, err)
		}
		_, copyErr := io.CopyN(file, tr, header.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("extract embedded build asset %q: %w", header.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close embedded build asset %q: %w", header.Name, closeErr)
		}
	}
	return nil
}
