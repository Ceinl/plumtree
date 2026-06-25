package buildworker

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stdOnlyProject is a minimal SDK-shaped app that imports nothing external, so
// it compiles to wasip1 with GOPROXY=off and no module cache seeding.
var stdOnlyProject = map[string]string{
	"go.mod":        "module example.com/app\n\ngo 1.26\n",
	"plumtree.json": `{"name":"demo","type":"cli"}`,
	"app/main.go":   "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }\n",
}

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func packProject(t *testing.T, files map[string]string) []byte {
	t.Helper()
	archive, err := PackSource(writeProject(t, files))
	if err != nil {
		t.Fatalf("PackSource: %v", err)
	}
	return archive
}

// testBuilder skips the module allowlist (std-only programs declare no requires
// anyway, but several negative tests use synthetic requires that should fail the
// compile, not the policy, unless the test sets a policy explicitly).
func testBuilder(cfg Config) *Builder {
	if cfg.AllowedModules == nil {
		cfg.AllowedModules = []string{}
	}
	return NewBuilder(cfg)
}

func TestBuildSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a real wasip1 binary")
	}
	archive := packProject(t, stdOnlyProject)
	b := testBuilder(Config{})
	res, err := b.Build(context.Background(), Request{Source: archive, ABIVersion: 0})
	if err != nil {
		t.Fatalf("Build worker error: %v", err)
	}
	if !res.Success {
		t.Fatalf("build failed: %+v\nlog:\n%s", res.Failure, res.BuildLog)
	}
	if len(res.WASM) == 0 {
		t.Fatal("empty WASM output")
	}
	if res.Digest != SourceDigest(res.WASM) {
		t.Errorf("digest %q does not match content address %q", res.Digest, SourceDigest(res.WASM))
	}
	if res.SizeBytes != int64(len(res.WASM)) {
		t.Errorf("SizeBytes %d != len(WASM) %d", res.SizeBytes, len(res.WASM))
	}
	if res.CompilerVersion == "" {
		t.Error("missing compiler version")
	}
	// WASM modules start with the "\0asm" magic.
	if !bytes.HasPrefix(res.WASM, []byte("\x00asm")) {
		t.Error("output is not a WASM module")
	}
}

func TestBuildDeterministicDigest(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a real wasip1 binary")
	}
	archive := packProject(t, stdOnlyProject)
	b := testBuilder(Config{})
	r1, err := b.Build(context.Background(), Request{Source: archive})
	if err != nil || !r1.Success {
		t.Fatalf("build 1: err=%v failure=%+v", err, r1.Failure)
	}
	r2, err := b.Build(context.Background(), Request{Source: archive})
	if err != nil || !r2.Success {
		t.Fatalf("build 2: err=%v failure=%+v", err, r2.Failure)
	}
	if r1.Digest != r2.Digest {
		t.Errorf("non-reproducible build: %s != %s", r1.Digest, r2.Digest)
	}
}

func TestBuildSourceTooLarge(t *testing.T) {
	archive := packProject(t, stdOnlyProject)
	b := testBuilder(Config{MaxSourceBytes: 10})
	res, err := b.Build(context.Background(), Request{Source: archive})
	if err != nil {
		t.Fatalf("worker error: %v", err)
	}
	if res.Success || res.Failure == nil || res.Failure.Stage != StageSource {
		t.Fatalf("expected source-stage failure, got %+v", res)
	}
}

func TestBuildCompileError(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes the go toolchain")
	}
	files := map[string]string{
		"go.mod":      "module example.com/app\n\ngo 1.26\n",
		"app/main.go": "package main\n\nfunc main() { this is not go }\n",
	}
	archive := packProject(t, files)
	b := testBuilder(Config{})
	res, err := b.Build(context.Background(), Request{Source: archive})
	if err != nil {
		t.Fatalf("worker error: %v", err)
	}
	if res.Success || res.Failure == nil || res.Failure.Stage != StageCompile {
		t.Fatalf("expected compile failure, got %+v", res)
	}
	if res.Failure.Log == "" {
		t.Error("compile failure should carry the build log")
	}
}

func TestBuildModulePolicyRejection(t *testing.T) {
	files := map[string]string{
		"go.mod":      "module example.com/app\n\ngo 1.26\n\nrequire github.com/evil/pkg v1.0.0\n",
		"app/main.go": "package main\n\nfunc main() {}\n",
	}
	archive := packProject(t, files)
	// Use the default allowlist, which does not include github.com/evil/pkg.
	b := NewBuilder(Config{})
	res, err := b.Build(context.Background(), Request{Source: archive})
	if err != nil {
		t.Fatalf("worker error: %v", err)
	}
	if res.Success || res.Failure == nil || res.Failure.Stage != StagePolicy {
		t.Fatalf("expected policy failure, got %+v", res)
	}
}

func TestBuildToolchainDirectiveRejected(t *testing.T) {
	files := map[string]string{
		"go.mod":      "module example.com/app\n\ngo 1.26\n\ntoolchain go1.99.0\n",
		"app/main.go": "package main\n\nfunc main() {}\n",
	}
	archive := packProject(t, files)
	b := NewBuilder(Config{})
	res, err := b.Build(context.Background(), Request{Source: archive})
	if err != nil {
		t.Fatalf("worker error: %v", err)
	}
	if res.Success || res.Failure == nil || res.Failure.Stage != StagePolicy {
		t.Fatalf("expected policy failure for toolchain directive, got %+v", res)
	}
}

func TestBuildTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes the go toolchain")
	}
	archive := packProject(t, stdOnlyProject)
	b := testBuilder(Config{Timeout: time.Nanosecond})
	res, err := b.Build(context.Background(), Request{Source: archive})
	if err != nil {
		t.Fatalf("worker error: %v", err)
	}
	if res.Success || res.Failure == nil || res.Failure.Stage != StageTimeout {
		t.Fatalf("expected timeout failure, got %+v", res)
	}
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	payload := []byte("pwned")
	_ = tw.WriteHeader(&tar.Header{Name: "../escape.go", Mode: 0o644, Size: int64(len(payload))})
	_, _ = tw.Write(payload)
	_ = tw.Close()

	dst := t.TempDir()
	if _, err := extractSource(buf.Bytes(), dst, 1<<20, 100); err == nil {
		t.Fatal("expected path-traversal rejection")
	}
}

func TestExtractEnforcesFileCount(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := 0; i < 5; i++ {
		name := filepath.ToSlash(filepath.Join("app", "f"+string(rune('a'+i))+".go"))
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 1})
		_, _ = tw.Write([]byte("x"))
	}
	_ = tw.Close()
	if _, err := extractSource(buf.Bytes(), t.TempDir(), 1<<20, 3); err == nil {
		t.Fatal("expected file-count rejection")
	}
}
