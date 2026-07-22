package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	buildworker "github.com/Ceinl/plumtree/build-worker"
)

func TestEmbeddedBuildBackendBuildsScaffoldedApps(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles real wasip1 applications")
	}
	backend, cleanup, err := buildBackend("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })

	projects := map[string]string{
		"cli": `package main

import "github.com/Ceinl/plumtree/sdk"

func main() {
	sdk.CLI(sdk.Meta{Name: "embedded-cli", Type: "cli"}, func(ctx sdk.Ctx, args []string) error {
		ctx.Out().Println("hello")
		return nil
	})
}
`,
		"tui": `package main

import (
	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

type model struct{}

func (*model) Update(sdk.Event) {}
func (*model) View() tui.Component { return components.NewText("hello") }

func main() { sdk.RunTUI(&model{}, sdk.Meta{Name: "embedded-tui", Type: "tui"}) }
`,
	}

	for name, mainGo := range projects {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "go.mod", "module example.com/"+name+"\n\ngo 1.26.5\n\nrequire github.com/Ceinl/plumtree/sdk v0.0.0\n")
			writeTestFile(t, root, "plumtree.json", `{"name":"embedded-`+name+`","type":"`+name+`"}`)
			writeTestFile(t, root, "app/main.go", mainGo)
			source, err := buildworker.PackSource(root)
			if err != nil {
				t.Fatal(err)
			}
			result, err := backend.Build(context.Background(), buildworker.Request{Source: source})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Success {
				t.Fatalf("embedded build failed: %+v\n%s", result.Failure, result.BuildLog)
			}
		})
	}
}

func TestEmbeddedBuildBackendRequiresGoToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, err := buildBackend("", "", "")
	if err == nil || !strings.Contains(err.Error(), "Go toolchain on PATH") {
		t.Fatalf("buildBackend error = %v, want Go toolchain guidance", err)
	}
	if _, _, err := buildBackend("http://build-worker", "token", ""); err != nil {
		t.Fatalf("remote build backend should not require a local toolchain: %v", err)
	}
}

func TestWorkspaceModulesRequiresCompleteDevelopmentRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := workspaceModules(root); err == nil {
		t.Fatal("workspaceModules accepted an empty development root")
	}
	for _, module := range []string{"sdk", "tui-runtime"} {
		writeTestFile(t, root, filepath.Join(module, "go.mod"), "module example.com/"+module+"\n")
	}
	modules, err := workspaceModules(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 {
		t.Fatalf("workspace modules = %q, want sdk and tui-runtime", modules)
	}
}

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
