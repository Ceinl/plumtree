package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type manifest struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func buildWASM(proj string) ([]byte, func(), error) {
	out, err := os.CreateTemp("", "pt-dev-*.wasm")
	if err != nil {
		return nil, func() {}, err
	}
	out.Close()
	cleanup := func() { os.Remove(out.Name()) }

	env := append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if work, workCleanup, ok := devWorkspace(proj); ok {
		env = append(env, "GOWORK="+work)
		prev := cleanup
		cleanup = func() { prev(); workCleanup() }
	}

	cmd := exec.Command("go", "build", "-o", out.Name(), "./app")
	cmd.Dir = proj
	cmd.Env = env
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("compiling ./app to WASM: %w", err)
	}
	b, err := os.ReadFile(out.Name())
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return b, cleanup, nil
}

func devWorkspace(proj string) (path string, cleanup func(), ok bool) {
	root := os.Getenv("PLUMTREE_DEV_ROOT")
	if root == "" {
		root = devRoot
	}
	if root == "" {
		return "", nil, false
	}
	sdk := filepath.Join(root, "sdk")
	runtime := filepath.Join(root, "tui-runtime")
	if !isDir(sdk) || !isDir(runtime) {
		return "", nil, false
	}
	f, err := os.CreateTemp("", "pt-dev-*.work")
	if err != nil {
		return "", nil, false
	}
	content := fmt.Sprintf("go 1.26.5\n\nuse (\n\t%s\n\t%s\n\t%s\n)\n", proj, sdk, runtime)
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, false
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }, true
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
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
