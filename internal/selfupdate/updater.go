package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Ceinl/plumtree/internal/fsatomic"
)

// PairMembers are the matched binaries a release ships and the updater
// manages together. Either binary's update command refreshes every member
// found next to itself, so the pair never drifts.
var PairMembers = []string{"pt", "plumtree"}

// Target is one installable binary next to the running executable.
type Target struct {
	Name string
	Path string
	Mode os.FileMode
}

// swapFault is a test seam: tests inject it to force a mid-swap failure and
// observe the rollback behavior.
var swapFault func(Target) error

// PairTargets returns every matched binary present in the install directory.
func PairTargets(directory string) ([]Target, error) {
	targets := make([]Target, 0, len(PairMembers))
	for _, member := range PairMembers {
		path := filepath.Join(directory, member)
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		targets = append(targets, Target{Name: member, Path: path, Mode: info.Mode().Perm()})
	}
	return targets, nil
}

// ReadChecksums parses a sha256sum-format checksums.txt manifest into asset
// name → checksum.
func ReadChecksums(manifest []byte) (map[string]string, error) {
	checksums := make(map[string]string)
	for _, line := range strings.Split(string(manifest), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("checksum manifest line %q is not sha256sum format", line)
		}
		checksum, name := fields[0], filepath.Base(strings.TrimPrefix(fields[1], "*"))
		if len(checksum) != 64 || !isHex(checksum) {
			return nil, fmt.Errorf("checksum manifest line %q has no sha256 digest", line)
		}
		checksums[name] = strings.ToLower(checksum)
	}
	if len(checksums) == 0 {
		return nil, fmt.Errorf("checksum manifest is empty")
	}
	return checksums, nil
}

func isHex(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// Sha256Hex returns the hex sha256 digest of data.
func Sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// ReplaceAll swaps payloads into the targets one at a time. Each replaced
// binary is first renamed aside as <path>.ptbak; any failure renames the
// displaced originals back, so a half-updated pair is never the end state.
// The injected fault hook (tests only, nil in production) forces a mid-swap
// failure to observe the rollback.
func ReplaceAll(rootDirectory string, payloads map[string][]byte, fault func(Target) error) error {
	targets, err := PairTargets(rootDirectory)
	if err != nil {
		return fmt.Errorf("inspect install directory: %w", err)
	}
	swapped := make([]string, 0, len(targets))
	rollback := func(cause error) error {
		for _, path := range swapped {
			bak := path + ".ptbak"
			_ = os.Remove(path)
			if err := os.Rename(bak, path); err != nil {
				return fmt.Errorf("rollback of %s after %v: %w", path, cause, err)
			}
		}
		return cause
	}
	for _, target := range targets {
		if fault != nil {
			if err := fault(target); err != nil {
				return rollback(fmt.Errorf("replace %s: %w", target.Name, err))
			}
		}
		payload, found := payloads[target.Name]
		if !found {
			return rollback(fmt.Errorf("release is missing the %s asset", target.Name))
		}
		_ = os.Remove(target.Path + ".ptbak") // finish a previous displaced-binary cleanup
		if err := os.Rename(target.Path, target.Path+".ptbak"); err != nil {
			return rollback(fmt.Errorf("move %s aside: %w", target.Name, err))
		}
		if err := fsatomic.WriteFileAtomic(target.Path, payload, target.Mode); err != nil {
			return rollback(fmt.Errorf("write %s: %w", target.Name, err))
		}
		swapped = append(swapped, target.Path)
	}
	return nil
}

// CleanupDisplaced drops any displaced binaries left by earlier updates
// (Windows keeps the running one until after a later update).
func CleanupDisplaced(directory string) {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	for _, member := range PairMembers {
		_ = os.Remove(filepath.Join(directory, member+ext+".ptbak"))
	}
}

// IsWritable probes whether the directory accepts a created file without
// touching any existing content.
func IsWritable(directory string) error {
	handle, err := os.CreateTemp(directory, ".pt-update-probe-*")
	if err != nil {
		return err
	}
	name := handle.Name()
	_ = handle.Close()
	_ = os.Remove(name)
	return nil
}
