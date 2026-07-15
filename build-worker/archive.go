// Package buildworker compiles uploaded Go app source into WASM artifacts inside
// a constrained, network-free sandbox. The control plane never trusts the
// source: the worker enforces source-size, file-count, build-time, module
// policy, and cache-isolation limits, and returns either a content-addressed
// artifact or a structured build failure.
package buildworker

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// sourceRoots are the only top-level paths packed into a source archive. The
// app lives under app/; go.mod/go.sum/plumtree.json pin the build; vendor/ (when
// present) makes the build fully offline.
var sourceRoots = []string{"go.mod", "go.sum", "plumtree.json", "app", "vendor"}

// PackSource builds a deterministic tar archive of an app project for upload to
// the build worker. Only sourceRoots are included; file order is sorted so the
// archive bytes are reproducible for a given tree.
func PackSource(proj string) ([]byte, error) {
	files, err := collectSourceFiles(proj)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, rel := range files {
		full := filepath.Join(proj, filepath.FromSlash(rel))
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		hdr := &tar.Header{
			Name: rel,
			Mode: 0o644,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// collectSourceFiles returns the sorted slash-separated relative paths that
// PackSource includes, walking the directory roots recursively.
func collectSourceFiles(proj string) ([]string, error) {
	var files []string
	for _, root := range sourceRoots {
		full := filepath.Join(proj, root)
		info, err := os.Lstat(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("source root %q must not be a symlink", root)
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("source root %q must be a regular file or directory", root)
			}
			files = append(files, root)
			continue
		}
		if err := filepath.WalkDir(full, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(proj, p)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

// SourceDigest returns the content address of a packed source archive.
func SourceDigest(archive []byte) string {
	sum := sha256.Sum256(archive)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// extractSource unpacks a source archive into dst, enforcing total-size and
// file-count caps and rejecting any path that escapes dst. It returns the
// number of bytes written. Callers pass an already size-checked archive.
func extractSource(archive []byte, dst string, maxBytes int64, maxFiles int) (int64, error) {
	tr := tar.NewReader(bytes.NewReader(archive))
	var total int64
	var count int
	seen := make(map[string]bool)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return total, fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			// Skip directories/symlinks/devices: the packer only writes regular
			// files, and refusing the rest blocks symlink-escape tricks.
			continue
		}
		archivePath, err := normalizeSourcePath(hdr.Name)
		if err != nil {
			return total, err
		}
		if seen[archivePath] {
			return total, fmt.Errorf("duplicate archive entry %q", hdr.Name)
		}
		seen[archivePath] = true
		clean, err := safeJoin(dst, archivePath)
		if err != nil {
			return total, err
		}
		count++
		if maxFiles > 0 && count > maxFiles {
			return total, fmt.Errorf("archive exceeds %d files", maxFiles)
		}
		total += hdr.Size
		if maxBytes > 0 && total > maxBytes {
			return total, fmt.Errorf("archive exceeds %d extracted bytes", maxBytes)
		}
		if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
			return total, err
		}
		f, err := os.OpenFile(clean, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return total, err
		}
		limited := io.LimitReader(tr, hdr.Size)
		if _, err := io.Copy(f, limited); err != nil {
			f.Close()
			return total, err
		}
		if err := f.Close(); err != nil {
			return total, err
		}
	}
	return total, nil
}

func normalizeSourcePath(name string) (string, error) {
	norm := strings.ReplaceAll(name, `\`, "/")
	if path.IsAbs(norm) {
		return "", fmt.Errorf("archive entry %q is an absolute path", name)
	}
	for _, seg := range strings.Split(norm, "/") {
		if seg == ".." {
			return "", fmt.Errorf("archive entry %q escapes build root", name)
		}
	}
	clean := path.Clean(norm)
	if clean == "." {
		return "", fmt.Errorf("archive entry %q has no file name", name)
	}
	top, _, _ := strings.Cut(clean, "/")
	allowed := false
	for _, root := range sourceRoots {
		if top == root {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("archive entry %q is outside allowed source roots", name)
	}
	if (top == "go.mod" || top == "go.sum" || top == "plumtree.json") && clean != top {
		return "", fmt.Errorf("archive entry %q treats file root %q as a directory", name, top)
	}
	return clean, nil
}

// safeJoin resolves name under root, rejecting absolute paths and any ".."
// traversal segment outright rather than silently rewriting it.
func safeJoin(root, name string) (string, error) {
	norm := strings.ReplaceAll(name, `\`, "/")
	if path.IsAbs(norm) {
		return "", fmt.Errorf("archive entry %q is an absolute path", name)
	}
	for _, seg := range strings.Split(norm, "/") {
		if seg == ".." {
			return "", fmt.Errorf("archive entry %q escapes build root", name)
		}
	}
	joined := filepath.Join(root, filepath.FromSlash(path.Clean(norm)))
	rel, err := filepath.Rel(root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes build root", name)
	}
	return joined, nil
}
