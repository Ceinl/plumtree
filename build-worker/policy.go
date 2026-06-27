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
// require check (used by tests that build std-only programs), but the structural
// rejections below always apply. It rejects:
//   - toolchain directives (would trigger a network toolchain download),
//   - replace/exclude directives (a replace can redirect an allowlisted module to
//     malicious code or, via a filesystem path, pull host files into the build
//     through //go:embed; exclude can shift resolution off the allowlist).
func enforceModulePolicy(projDir string, allowed []string) error {
	data, err := os.ReadFile(filepath.Join(projDir, "go.mod"))
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	mod, err := parseGoMod(string(data))
	if err != nil {
		return err
	}
	if mod.toolchain != "" {
		return fmt.Errorf("go.mod toolchain directive %q is not allowed", mod.toolchain)
	}
	if mod.hasReplace {
		return fmt.Errorf("go.mod replace directives are not allowed")
	}
	if mod.hasExclude {
		return fmt.Errorf("go.mod exclude directives are not allowed")
	}
	if len(allowed) == 0 {
		return nil
	}
	for _, path := range mod.requires {
		if !moduleAllowed(path, allowed) {
			return fmt.Errorf("module %q is not on the build allowlist", path)
		}
	}
	return nil
}

// goMod is the minimal view of a go.mod the policy needs.
type goMod struct {
	requires   []string
	toolchain  string
	hasReplace bool
	hasExclude bool
}

// parseGoMod extracts required module paths, any toolchain directive, and the
// presence of replace/exclude directives from go.mod text. It handles both
// single-line (`require path version`) and block (`require (\n ... \n)`) forms
// for every directive, and strips // comments. It is intentionally minimal: the
// build is the source of truth, so this only needs to be strict enough to gate
// policy.
func parseGoMod(text string) (goMod, error) {
	var mod goMod
	sc := bufio.NewScanner(strings.NewReader(text))
	block := "" // current open block directive ("require"/"replace"/...), or ""
	for sc.Scan() {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		if block != "" {
			if line == ")" {
				block = ""
				continue
			}
			switch block {
			case "require":
				if path := firstField(line); path != "" {
					mod.requires = append(mod.requires, path)
				}
			case "replace":
				mod.hasReplace = true
			case "exclude":
				mod.hasExclude = true
			}
			continue
		}
		switch {
		case line == "require (":
			block = "require"
		case line == "replace (":
			block = "replace"
		case line == "exclude (":
			block = "exclude"
		case strings.HasPrefix(line, "require "):
			if path := firstField(strings.TrimPrefix(line, "require ")); path != "" {
				mod.requires = append(mod.requires, path)
			}
		case strings.HasPrefix(line, "replace "):
			mod.hasReplace = true
		case strings.HasPrefix(line, "exclude "):
			mod.hasExclude = true
		case strings.HasPrefix(line, "toolchain "):
			mod.toolchain = firstField(strings.TrimPrefix(line, "toolchain "))
		}
	}
	if err := sc.Err(); err != nil {
		return goMod{}, fmt.Errorf("read go.mod: %w", err)
	}
	return mod, nil
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
