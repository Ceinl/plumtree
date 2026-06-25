package buildworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Stage labels where in the pipeline a build failed, so pt deploy can present a
// useful message without parsing logs.
type Stage string

const (
	StageSource  Stage = "source"  // archive too large / malformed / path escape
	StagePolicy  Stage = "policy"  // module allowlist or toolchain rejection
	StageCompile Stage = "compile" // go build returned a non-zero status
	StageTimeout Stage = "timeout" // build exceeded the wall-clock budget
	StageWorker  Stage = "worker"  // internal worker error (not the author's fault)
)

// Config bounds a Builder. Zero fields fall back to the defaults applied by
// NewBuilder, so callers can override only what they care about.
type Config struct {
	// GoBin is the go toolchain binary. Defaults to "go" resolved on PATH.
	GoBin string
	// WorkRoot is the parent directory for per-build sandboxes. Defaults to the
	// OS temp dir. Each build gets an isolated subdirectory removed afterwards.
	WorkRoot string
	// MaxSourceBytes caps the uploaded archive size. Default 8 MiB.
	MaxSourceBytes int64
	// MaxExtractBytes caps total extracted source bytes. Default 64 MiB.
	MaxExtractBytes int64
	// MaxFiles caps the number of files in the archive. Default 2000.
	MaxFiles int
	// Timeout bounds total build wall-clock time. Default 90s.
	Timeout time.Duration
	// GoProxy sets GOPROXY for the build. Default "off" — no network. Set to a
	// trusted module mirror to allow restricted dependency resolution.
	GoProxy string
	// AllowedModules is the module path allowlist (see enforceModulePolicy).
	// Defaults to DefaultAllowedModules. Set to a non-nil empty slice to skip
	// the check (used for std-only test programs).
	AllowedModules []string
	// WorkspaceModules are local module directories (e.g. an unpublished SDK and
	// TUI runtime) tied into a generated go.work alongside the uploaded source so
	// the build resolves them without a published version. This is a local
	// development path; production resolves published modules through GoProxy.
	// When set, GOFLAGS uses -mod=mod and GoProxy should permit transitive
	// dependency downloads (it is not forced to "off").
	WorkspaceModules []string
	// ExtraEnv is appended to the hermetic build environment.
	ExtraEnv []string
}

// Builder compiles source archives to WASM under a fixed Config.
type Builder struct {
	cfg      Config
	goBinDir string // directory of the resolved go toolchain, added to sandbox PATH
}

// NewBuilder returns a Builder with defaults filled in for any zero Config field.
func NewBuilder(cfg Config) *Builder {
	if cfg.GoBin == "" {
		cfg.GoBin = "go"
	}
	if cfg.MaxSourceBytes == 0 {
		cfg.MaxSourceBytes = 8 << 20
	}
	if cfg.MaxExtractBytes == 0 {
		cfg.MaxExtractBytes = 64 << 20
	}
	if cfg.MaxFiles == 0 {
		cfg.MaxFiles = 2000
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 90 * time.Second
	}
	if cfg.GoProxy == "" {
		cfg.GoProxy = "off"
	}
	if cfg.AllowedModules == nil {
		cfg.AllowedModules = DefaultAllowedModules
	}
	b := &Builder{cfg: cfg}
	if resolved, err := exec.LookPath(cfg.GoBin); err == nil {
		b.goBinDir = filepath.Dir(resolved)
	}
	return b
}

// Request is one build job. Source marshals to base64 over JSON.
type Request struct {
	// Source is the tar archive produced by PackSource.
	Source []byte `json:"source"`
	// ABIVersion is recorded on the resulting artifact metadata.
	ABIVersion uint8 `json:"abiVersion"`
}

// Result is the outcome of a build. Exactly one of Failure / (WASM,Digest) is
// meaningful depending on Success.
type Result struct {
	Success         bool     `json:"success"`
	WASM            []byte   `json:"wasm,omitempty"`
	Digest          string   `json:"digest,omitempty"` // sha256:... content address of WASM
	SizeBytes       int64    `json:"sizeBytes,omitempty"`
	ABIVersion      uint8    `json:"abiVersion"`
	CompilerVersion string   `json:"compilerVersion,omitempty"`
	BuildLog        string   `json:"buildLog,omitempty"`
	DurationMillis  int64    `json:"durationMillis"`
	Failure         *Failure `json:"failure,omitempty"`
}

// Failure is a structured, author-facing build error.
type Failure struct {
	Stage   Stage  `json:"stage"`
	Message string `json:"message"`
	Log     string `json:"log,omitempty"`
}

func (f *Failure) Error() string { return string(f.Stage) + ": " + f.Message }

// Build compiles req.Source to a WASM artifact inside a fresh sandbox. A build
// that fails for an author-caused reason (oversized source, disallowed module,
// compile error, timeout) returns Result{Success:false, Failure:...} with a nil
// error; a nil error means the worker itself functioned. A non-nil error means
// the worker could not run the build at all.
func (b *Builder) Build(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	res := Result{ABIVersion: req.ABIVersion}

	if int64(len(req.Source)) > b.cfg.MaxSourceBytes {
		res.Failure = &Failure{Stage: StageSource, Message: fmt.Sprintf("source archive is %d bytes, over the %d byte limit", len(req.Source), b.cfg.MaxSourceBytes)}
		return res, nil
	}

	sandbox, err := os.MkdirTemp(b.cfg.WorkRoot, "ptbuild-*")
	if err != nil {
		return res, fmt.Errorf("create sandbox: %w", err)
	}
	defer os.RemoveAll(sandbox)

	srcDir := filepath.Join(sandbox, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return res, fmt.Errorf("create source dir: %w", err)
	}
	if _, err := extractSource(req.Source, srcDir, b.cfg.MaxExtractBytes, b.cfg.MaxFiles); err != nil {
		res.Failure = &Failure{Stage: StageSource, Message: err.Error()}
		res.DurationMillis = time.Since(start).Milliseconds()
		return res, nil
	}

	if err := enforceModulePolicy(srcDir, b.cfg.AllowedModules); err != nil {
		res.Failure = &Failure{Stage: StagePolicy, Message: err.Error()}
		res.DurationMillis = time.Since(start).Milliseconds()
		return res, nil
	}

	res.CompilerVersion = b.compilerVersion(ctx, sandbox)

	wasmPath := filepath.Join(sandbox, "out.wasm")
	log, stage, err := b.compile(ctx, sandbox, srcDir, wasmPath)
	res.BuildLog = log
	res.DurationMillis = time.Since(start).Milliseconds()
	if err != nil {
		res.Failure = &Failure{Stage: stage, Message: err.Error(), Log: log}
		return res, nil
	}

	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		return res, fmt.Errorf("read build output: %w", err)
	}
	sum := sha256.Sum256(wasm)
	res.Success = true
	res.WASM = wasm
	res.Digest = "sha256:" + hex.EncodeToString(sum[:])
	res.SizeBytes = int64(len(wasm))
	return res, nil
}

// compile runs `go build` for wasip1/wasm in the sandbox and returns the
// combined log. On failure it classifies the stage as timeout or compile.
func (b *Builder) compile(ctx context.Context, sandbox, srcDir, wasmPath string) (log string, stage Stage, err error) {
	buildCtx, cancel := context.WithTimeout(ctx, b.cfg.Timeout)
	defer cancel()

	goWork, err := b.writeWorkspace(sandbox, srcDir)
	if err != nil {
		return "", StageWorker, err
	}

	cmd := exec.CommandContext(buildCtx, b.cfg.GoBin, "build", "-trimpath", "-o", wasmPath, "./app")
	cmd.Dir = srcDir
	cmd.Env = b.buildEnv(sandbox, srcDir, goWork)
	configureSandboxProc(cmd)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	log = out.String()
	if runErr == nil {
		return log, "", nil
	}
	if buildCtx.Err() == context.DeadlineExceeded {
		return log, StageTimeout, fmt.Errorf("build exceeded %s time limit", b.cfg.Timeout)
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return log, StageCompile, errors.New("go build failed")
	}
	return log, StageWorker, fmt.Errorf("run go build: %w", runErr)
}

// buildEnv constructs a hermetic environment from scratch — it never inherits
// the worker process environment, so no build-time secrets can leak into
// untrusted source. Caches live inside the sandbox so builds cannot share or
// poison state, and GOPROXY/GOFLAGS pin offline, checksummed module behavior.
func (b *Builder) buildEnv(sandbox, srcDir, goWork string) []string {
	gocache := filepath.Join(sandbox, "gocache")
	gomodcache := filepath.Join(sandbox, "gomodcache")
	gotmp := filepath.Join(sandbox, "gotmp")
	gopath := filepath.Join(sandbox, "gopath")
	for _, d := range []string{gocache, gomodcache, gotmp, gopath} {
		_ = os.MkdirAll(d, 0o755)
	}

	env := []string{
		"PATH=" + b.sandboxPATH(),
		"HOME=" + sandbox,
		"GOOS=wasip1",
		"GOARCH=wasm",
		"CGO_ENABLED=0",
		"GOCACHE=" + gocache,
		"GOMODCACHE=" + gomodcache,
		"GOPATH=" + gopath,
		"GOTMPDIR=" + gotmp,
		"GOENV=off",
		"GOPROXY=" + b.cfg.GoProxy,
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
	}
	if goWork != "" {
		// Workspace mode rejects -mod=mod; let go pick its default (readonly).
		env = append(env, "GOWORK="+goWork)
	} else {
		mod := "mod"
		if _, err := os.Stat(filepath.Join(srcDir, "vendor", "modules.txt")); err == nil {
			mod = "vendor" // vendored source builds fully offline
		}
		// No workspace: keep module resolution self-contained.
		env = append(env, "GOWORK=off", "GOFLAGS=-mod="+mod)
	}
	env = append(env, b.cfg.ExtraEnv...)
	return env
}

// writeWorkspace generates a go.work in the sandbox tying the uploaded source
// module to the configured local WorkspaceModules, mirroring how `pt dev`
// resolves the unpublished SDK. It returns an empty path (and no file) when no
// workspace modules are configured, leaving the build in plain module mode.
func (b *Builder) writeWorkspace(sandbox, srcDir string) (string, error) {
	if len(b.cfg.WorkspaceModules) == 0 {
		return "", nil
	}
	var sb strings.Builder
	sb.WriteString("go 1.26\n\nuse (\n")
	sb.WriteString("\t" + resolvePath(srcDir) + "\n")
	for _, m := range b.cfg.WorkspaceModules {
		sb.WriteString("\t" + resolvePath(m) + "\n")
	}
	sb.WriteString(")\n")
	path := filepath.Join(sandbox, "go.work")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return "", fmt.Errorf("write workspace: %w", err)
	}
	return path, nil
}

// resolvePath canonicalizes a workspace path so go.work `use` entries match the
// module roots the toolchain resolves (macOS temp dirs symlink through
// /private/var, which otherwise fails the workspace membership check).
func resolvePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// compilerVersion records the toolchain identity for build metadata. Failures
// are non-fatal: an empty string just omits the field.
func (b *Builder) compilerVersion(ctx context.Context, sandbox string) string {
	cmd := exec.CommandContext(ctx, b.cfg.GoBin, "version")
	cmd.Env = []string{"PATH=" + b.sandboxPATH(), "HOME=" + sandbox, "GOENV=off"}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sandboxPATH returns a conservative PATH for locating the go toolchain and its
// linker without exposing the worker's full PATH to the build. The resolved go
// binary directory is prepended so the build finds the same toolchain the
// worker is configured with.
func (b *Builder) sandboxPATH() string {
	base := "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	if p := os.Getenv("PLUMTREE_BUILD_PATH"); p != "" {
		base = p
	}
	if b.goBinDir != "" {
		return b.goBinDir + string(os.PathListSeparator) + base
	}
	return base
}
