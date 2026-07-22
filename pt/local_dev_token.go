package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxLocalDevTokenBytes = 4 << 10

func localDevTokenPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("PLUMTREE_DEV_TOKEN_FILE")); path != "" {
		return filepath.Abs(path)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "plumtree", "dev-token"), nil
}

func readLocalDevToken() (string, error) {
	path, err := localDevTokenPath()
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect local dev token %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("local dev token %q must be a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("local dev token %q has insecure permissions %04o; run chmod 600 %q", path, info.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read local dev token %q: %w", path, err)
	}
	if len(b) > maxLocalDevTokenBytes {
		return "", fmt.Errorf("local dev token %q exceeds %d bytes", path, maxLocalDevTokenBytes)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("local dev token %q is empty", path)
	}
	return token, nil
}
