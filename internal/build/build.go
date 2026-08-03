// Package build owns author-side local application builds. The isolated
// source build-worker remains a separate server-side module.
package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Project identifies an app source tree.
type Project struct {
	Root string
}

// Artifact is the locally built WebAssembly module.
type Artifact struct {
	WASM []byte
}

// Builder compiles a project to wasip1/wasm.
type Builder interface {
	Build(context.Context, Project) (Artifact, error)
}

// LocalBuilder invokes the local Go toolchain. WorkspaceRoot may point at a
// Plumtree checkout so development builds resolve the checkout SDK.
type LocalBuilder struct {
	GoBin         string
	WorkspaceRoot string
}

// Build compiles project.Root/app and returns the resulting WASM bytes.
func (b LocalBuilder) Build(ctx context.Context, project Project) (Artifact, error) {
	if project.Root == "" {
		return Artifact{}, errors.New("build app: project root is required")
	}
	goBin := b.GoBin
	if goBin == "" {
		goBin = "go"
	}
	if _, err := exec.LookPath(goBin); err != nil {
		return Artifact{}, fmt.Errorf("build app: Go toolchain %q is not available: %w", goBin, err)
	}

	out, err := os.CreateTemp("", "pt-dev-*.wasm")
	if err != nil {
		return Artifact{}, err
	}
	outPath := out.Name()
	if err := out.Close(); err != nil {
		_ = os.Remove(outPath)
		return Artifact{}, err
	}
	cleanup := func() { _ = os.Remove(outPath) }
	defer cleanup()

	env := append([]string{}, os.Environ()...)
	env = replaceEnv(env, "GOOS", "wasip1")
	env = replaceEnv(env, "GOARCH", "wasm")
	if b.WorkspaceRoot != "" {
		if work, workCleanup, ok := developmentWorkspace(project.Root, b.WorkspaceRoot); ok {
			defer workCleanup()
			env = replaceEnv(env, "GOWORK", work)
		}
	}

	cmd := exec.CommandContext(ctx, goBin, "build", "-o", outPath, "./app")
	cmd.Dir = project.Root
	cmd.Env = env
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return Artifact{}, fmt.Errorf("compiling ./app to WASM: %w", err)
	}
	wasm, err := os.ReadFile(outPath)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{WASM: wasm}, nil
}

func developmentWorkspace(projectRoot, plumtreeRoot string) (string, func(), bool) {
	sdk := filepath.Join(plumtreeRoot, "sdk")
	if !isDir(sdk) {
		return "", func() {}, false
	}
	file, err := os.CreateTemp("", "pt-dev-*.work")
	if err != nil {
		return "", func() {}, false
	}
	content := fmt.Sprintf("go 1.26.5\n\nuse (\n\t%s\n\t%s\n)\n", projectRoot, sdk)
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", func() {}, false
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", func() {}, false
	}
	return file.Name(), func() { _ = os.Remove(file.Name()) }, true
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func replaceEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	found := false
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if name == key {
			if !found {
				out = append(out, key+"="+value)
				found = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !found {
		out = append(out, key+"="+value)
	}
	return out
}
