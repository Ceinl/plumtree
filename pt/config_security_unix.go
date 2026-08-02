//go:build !windows

package main

import (
	"fmt"
	"os"
)

func validatePTConfigSecurity(path string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("pt config %q has insecure permissions %04o; run chmod 600 %q", path, info.Mode().Perm(), path)
	}
	return nil
}

func securePTConfigFile(path string) error {
	return os.Chmod(path, 0o600)
}
