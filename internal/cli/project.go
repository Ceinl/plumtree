package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	plumbuild "github.com/Ceinl/plumtree/internal/build"
)

type manifest struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func buildWASM(proj string) ([]byte, func(), error) {
	root := os.Getenv("PLUMTREE_DEV_ROOT")
	if root == "" {
		root = DevRoot
	}
	artifact, err := (plumbuild.LocalBuilder{WorkspaceRoot: root}).Build(context.Background(), plumbuild.Project{Root: proj})
	if err != nil {
		return nil, func() {}, err
	}
	return artifact.WASM, func() {}, nil
}

func findProject() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "plumtree.json")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no plumtree.json found; run pt dev from inside an app directory")
		}
		dir = parent
	}
}

func readManifest(proj string) (manifest, error) {
	var m manifest
	b, err := os.ReadFile(filepath.Join(proj, "plumtree.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("plumtree.json: %w", err)
	}
	if m.Type == "" {
		m.Type = "tui"
	}
	return m, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func splitTokens(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}
