//go:build !windows

package cli

import (
	"fmt"
	"os"
)

func validatePTConfigSecurity(path string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("has insecure permissions %04o; run chmod 600", info.Mode().Perm())
	}
	return nil
}

func securePTConfigFile(path string) error {
	return os.Chmod(path, 0o600)
}
