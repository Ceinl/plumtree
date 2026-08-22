// Package build owns author-side local application builds. The server receives
// the resulting typed artifact through the clean /api/v1 deployment surface.
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
	buildRoot, err := canonicalPath(project.Root)
	if err != nil {
		return Artifact{}, fmt.Errorf("build app: resolve project root: %w", err)
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
	env = replaceEnv(env, "PWD", buildRoot)
	configuredWorkspace := false
	workPath := ""
	if b.WorkspaceRoot != "" {
		if work, workCleanup, ok := developmentWorkspace(buildRoot, b.WorkspaceRoot); ok {
			defer workCleanup()
			env = replaceEnv(env, "GOWORK", work)
			workPath = work
			configuredWorkspace = true
		}
	}
	if !configuredWorkspace {
		bundle, err := Extract()
		if err != nil {
			return Artifact{}, fmt.Errorf("build app: extract embedded SDK: %w", err)
		}
		defer bundle.Cleanup()
		work, workCleanup, err := releaseWorkspace(buildRoot, bundle.WorkspaceModules)
		if err != nil {
			return Artifact{}, fmt.Errorf("build app: create embedded SDK workspace: %w", err)
		}
		defer workCleanup()
		env = replaceEnv(env, "GOWORK", work)
		workPath = work
		env = replaceEnv(env, "GOPROXY", bundle.GoProxy)
		env = replaceEnv(env, "GOSUMDB", "off")
	}

	cmd := exec.CommandContext(ctx, goBin, "build", "-o", outPath, "./app")
	cmd.Dir = buildRoot
	cmd.Env = env
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return Artifact{}, fmt.Errorf("compiling ./app to WASM in %s with workspace %s: %w", buildRoot, workPath, err)
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
	work, cleanup, err := releaseWorkspace(projectRoot, []string{sdk})
	return work, cleanup, err == nil
}

func releaseWorkspace(projectRoot string, modules []string) (string, func(), error) {
	projectRoot, err := canonicalPath(projectRoot)
	if err != nil {
		return "", func() {}, err
	}
	for i := range modules {
		modules[i], err = canonicalPath(modules[i])
		if err != nil {
			return "", func() {}, err
		}
	}
	file, err := os.CreateTemp("", "pt-dev-*.work")
	if err != nil {
		return "", func() {}, err
	}
	var content strings.Builder
	content.WriteString("go 1.26.5\n\nuse (\n\t")
	content.WriteString(projectRoot)
	for _, module := range modules {
		content.WriteString("\n\t")
		content.WriteString(module)
	}
	content.WriteString("\n)\n")
	if _, err := file.WriteString(content.String()); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", func() {}, err
	}
	return file.Name(), func() { _ = os.Remove(file.Name()) }, nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
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
