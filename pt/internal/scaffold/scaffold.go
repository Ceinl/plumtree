// Package scaffold generates the standard Plumtree app layout for `pt new`:
//
//	<name>/
//	  go.mod
//	  plumtree.json                 committed app manifest
//	  .gitignore                    ignores local env and deploy metadata
//	  README.md                     human-facing project quickstart
//	  AGENTS.md                     agent-facing project instructions
//	  app/main.go                   the app entrypoint (TUI or CLI)
package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	scaffoldtemplate "github.com/Ceinl/plumtree/pt/template"
)

// Kind is the app type.
type Kind string

const (
	TUI Kind = "tui"
	CLI Kind = "cli"
)

// nameRE is the strict app-name grammar: lowercase alphanumerics and single
// dashes, 1–40 chars, not starting or ending with a dash. No path separators,
// no shell metacharacters — names flow into handles and must stay safe.
var nameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// ValidateName reports whether name is an acceptable app handle.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid app name %q: use 1–40 lowercase letters, digits, and dashes (not leading/trailing)", name)
	}
	return nil
}

// New scaffolds an app named name of the given kind under parentDir, creating
// the directory <parentDir>/<name>. It refuses to overwrite an existing
// non-empty directory. It returns the created project directory.
func New(parentDir, name string, kind Kind) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if kind != TUI && kind != CLI {
		return "", fmt.Errorf("unknown app kind %q (want tui or cli)", kind)
	}

	dir := filepath.Join(parentDir, name)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("directory %s already exists and is not empty", dir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	files, err := renderTemplate(name, kind)
	if err != nil {
		return "", err
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

type templateData struct {
	Name         string
	Kind         string
	RunCommand   string
	CheckCommand string
}

func renderTemplate(name string, kind Kind) (map[string]string, error) {
	data := templateData{
		Name:         name,
		Kind:         string(kind),
		RunCommand:   "pt dev",
		CheckCommand: `pt dev --headless --script "up,up,down,q"`,
	}
	if kind == CLI {
		data.RunCommand = "pt dev Alice"
		data.CheckCommand = "pt dev Alice"
	}

	files := make(map[string]string)
	for _, root := range []string{"base", string(kind)} {
		if err := renderTemplateRoot(files, root, data); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func renderTemplateRoot(out map[string]string, root string, data templateData) error {
	return fs.WalkDir(scaffoldtemplate.Files, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".tmpl") {
			return fmt.Errorf("template file %s must use .tmpl suffix", path)
		}

		src, err := scaffoldtemplate.Files.ReadFile(path)
		if err != nil {
			return err
		}
		tmpl, err := template.New(path).Parse(string(src))
		if err != nil {
			return err
		}

		var rendered bytes.Buffer
		if err := tmpl.Execute(&rendered, data); err != nil {
			return err
		}

		rel, err := filepath.Rel(root, strings.TrimSuffix(path, ".tmpl"))
		if err != nil {
			return err
		}
		out[rel] = rendered.String()
		return nil
	})
}
