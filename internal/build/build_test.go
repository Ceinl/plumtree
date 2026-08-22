package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalBuilderFallsBackWhenWorkspaceRootIsInvalid(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.test/app\n\ngo 1.26.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	appDir := filepath.Join(project, "app")
	if err := os.Mkdir(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	artifact, err := (LocalBuilder{WorkspaceRoot: filepath.Join(t.TempDir(), "missing")}).Build(context.Background(), Project{Root: project})
	if err != nil {
		t.Fatalf("build with invalid development root: %v", err)
	}
	if len(artifact.WASM) < 4 || string(artifact.WASM[:4]) != "\x00asm" {
		t.Fatal("build did not return a WebAssembly module")
	}
}

func TestReleaseBuilderUsesEmbeddedSDK(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module release-app\n\ngo 1.26.5\n\nrequire github.com/Ceinl/plumtree/sdk v0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	appDir := filepath.Join(project, "app")
	if err := os.Mkdir(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	main := `package main

import (
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type model struct{}
func (*model) Update(app.Event) app.Command { return app.Quit() }
func (*model) View() ui.Node { return ui.Text("release") }
func main() { app.Run(&model{}) }
`
	if err := os.WriteFile(filepath.Join(appDir, "main.go"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}

	artifact, err := (LocalBuilder{}).Build(context.Background(), Project{Root: project})
	if err != nil {
		t.Fatalf("build with embedded release SDK: %v", err)
	}
	if len(artifact.WASM) < 4 || string(artifact.WASM[:4]) != "\x00asm" {
		t.Fatal("build did not return a WebAssembly module")
	}
}
