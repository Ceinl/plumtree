package build

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

var sourceRoots = []string{"go.mod", "go.sum", "plumtree.json", "app", "vendor"}

// PackSource returns a deterministic archive of the files that identify an
// app. It is used by the author CLI to record the deploy source digest.
func PackSource(projectRoot string) ([]byte, error) {
	files, err := collectSourceFiles(projectRoot)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	for _, relative := range files {
		full := filepath.Join(projectRoot, filepath.FromSlash(relative))
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		header := &tar.Header{Name: relative, Mode: 0o644, Size: int64(len(data))}
		if err := writer.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := writer.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func collectSourceFiles(projectRoot string) ([]string, error) {
	var files []string
	for _, root := range sourceRoots {
		full := filepath.Join(projectRoot, root)
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
		if err := filepath.WalkDir(full, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			relative, err := filepath.Rel(projectRoot, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(relative))
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

// SourceDigest returns the SHA-256 content address of a packed source archive.
func SourceDigest(source []byte) string {
	sum := sha256.Sum256(source)
	return "sha256:" + hex.EncodeToString(sum[:])
}
