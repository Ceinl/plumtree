package buildworker

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultAllowedModules is the v1 module allowlist: the standard library
// (implicit, never in go.mod), the Plumtree SDK, and the extracted TUI runtime.
// Apps may only require modules whose path is, or sits beneath, one of these.
var DefaultAllowedModules = []string{
	"github.com/Ceinl/plumtree/sdk",
	"github.com/Ceinl/plumtree/tui-runtime",
	"golang.org/x/sys",
	"golang.org/x/text",
}

// enforceModulePolicy parses the project's go.mod and rejects any required
// module whose path is not covered by allowed. An empty allowlist disables the
// check (used by tests that build std-only programs). It also rejects toolchain
// directives, which would trigger a network toolchain download.
func enforceModulePolicy(projDir string, allowed []string) error {
	data, err := os.ReadFile(filepath.Join(projDir, "go.mod"))
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	reqs, toolchain, err := parseGoMod(string(data))
	if err != nil {
		return err
	}
	if toolchain != "" {
		return fmt.Errorf("go.mod toolchain directive %q is not allowed", toolchain)
	}
	if len(allowed) == 0 {
		return nil
	}
	for _, path := range reqs {
		if !moduleAllowed(path, allowed) {
			return fmt.Errorf("module %q is not on the build allowlist", path)
		}
	}
	return nil
}

// parseGoMod extracts required module paths and any toolchain directive from
// go.mod text. It handles both single-line `require path version` and block
// `require (\n ... \n)` forms, and strips // comments. It is intentionally
// minimal: the build itself is the source of truth, so this only needs to be
// strict enough to gate the module allowlist.
func parseGoMod(text string) (requires []string, toolchain string, err error) {
	sc := bufio.NewScanner(strings.NewReader(text))
	inRequire := false
	for sc.Scan() {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		if inRequire {
			if line == ")" {
				inRequire = false
				continue
			}
			if path := firstField(line); path != "" {
				requires = append(requires, path)
			}
			continue
		}
		switch {
		case line == "require (":
			inRequire = true
		case strings.HasPrefix(line, "require "):
			if path := firstField(strings.TrimPrefix(line, "require ")); path != "" {
				requires = append(requires, path)
			}
		case strings.HasPrefix(line, "toolchain "):
			toolchain = firstField(strings.TrimPrefix(line, "toolchain "))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, "", fmt.Errorf("read go.mod: %w", err)
	}
	return requires, toolchain, nil
}

func stripComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

func firstField(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// moduleAllowed reports whether path equals or is a subpath of any allowed
// prefix. Matching is on path segments so "github.com/Ceinl/plumtree/sdkx" does not match
// "github.com/Ceinl/plumtree/sdk".
func moduleAllowed(path string, allowed []string) bool {
	for _, a := range allowed {
		if path == a || strings.HasPrefix(path, a+"/") {
			return true
		}
	}
	return false
}
