package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Commander is the high-authority capability that executes a local process.
// It must never be installed for an untrusted app or by default.
type Commander interface {
	Run(context.Context, abi.ExecRequest) (abi.ExecResponse, error)
}

const (
	// DefaultExecTimeout bounds each host command when LocalCommander.Timeout
	// is unset. A guest can never tie up a server process indefinitely.
	DefaultExecTimeout = 30 * time.Second
	// execChildPATH is the fixed PATH used both to resolve allowlisted bare
	// names and inside the child environment, so the gateway's own environment
	// cannot redirect an allowlisted command to an attacker-chosen binary.
	execChildPATH = "/usr/bin:/bin:/usr/sbin:/sbin"
)

// shellInterpreterNames are refused even when an operator allowlists them:
// a shell turns bounded argument vectors back into arbitrary execution.
var shellInterpreterNames = map[string]struct{}{
	"sh": {}, "bash": {}, "zsh": {}, "dash": {}, "ksh": {}, "csh": {},
	"fish": {}, "cmd": {}, "powershell": {}, "pwsh": {},
}

var (
	ErrExecTooLarge   = errors.New("runner: host command output too large")
	ErrExecDenied     = errors.New("runner: host command not permitted by operator allowlist")
	ErrExecTimedOut   = errors.New("runner: host command timed out")
	ErrExecNotFound   = errors.New("runner: host command not found in the sandbox PATH")
	ErrExecUnresolved = errors.New("runner: host command path could not be resolved")
)

// LocalCommander executes programs as the server OS user under a strict
// operator-configured allowlist with deny-by-default isolation. Every exec runs
// in a fresh temporary working directory with a minimal environment, and each
// command is bounded by Timeout. An empty allowlist refuses every command.
type LocalCommander struct {
	// Allowlist authorizes executables. A bare-name entry (no path separators)
	// authorizes only a bare-name request, resolved against the fixed sandbox
	// PATH. An absolute-path entry authorizes only the identical cleaned
	// absolute path — never the bare base name.
	Allowlist []string
	// WorkDir overrides the fresh per-exec working directory. The directory is
	// removed again after the command completes unless the operator supplied it.
	WorkDir string
	// Timeout bounds each exec. Zero selects DefaultExecTimeout.
	Timeout time.Duration
}

func (c LocalCommander) Run(ctx context.Context, req abi.ExecRequest) (abi.ExecResponse, error) {
	if !validExecRequest(req) {
		return abi.ExecResponse{}, ErrExecTooLarge
	}
	path, err := c.authorize(req.Name)
	if err != nil {
		return abi.ExecResponse{}, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dir := c.WorkDir
	if dir == "" {
		fresh, err := os.MkdirTemp("", "plumtree-exec-")
		if err != nil {
			return abi.ExecResponse{}, err
		}
		defer os.RemoveAll(fresh)
		dir = fresh
	}
	cmd := exec.CommandContext(cmdCtx, path, req.Args...)
	cmd.Dir = dir
	cmd.Env = minimalExecEnvironment()
	configureCommandGroup(cmd)
	stdout := &execBuffer{max: abi.ExecMaxOutput, cancel: cancel}
	stderr := &execBuffer{max: abi.ExecMaxOutput, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if stdout.overflow || stderr.overflow {
		return abi.ExecResponse{}, ErrExecTooLarge
	}
	if ctx.Err() != nil {
		return abi.ExecResponse{}, ctx.Err()
	}
	if cmdCtx.Err() != nil {
		return abi.ExecResponse{}, fmt.Errorf("%w after %s", ErrExecTimedOut, timeout)
	}
	resp := abi.ExecResponse{ExitCode: 0, Stdout: stdout.b, Stderr: stderr.b}
	if err == nil {
		return resp, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		resp.ExitCode = exitErr.ExitCode()
		return resp, nil
	}
	return abi.ExecResponse{}, err
}

// authorize maps a requested executable name to the concrete path this commander
// will run, failing closed unless the operator allowlist admits exactly that
// program and the program is not a shell interpreter.
func (c LocalCommander) authorize(name string) (string, error) {
	if len(c.Allowlist) == 0 {
		return "", ErrExecDenied
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\\') {
		cleaned := filepath.Clean(name)
		if !filepath.IsAbs(cleaned) {
			return "", ErrExecDenied
		}
		for _, entry := range c.Allowlist {
			entry = strings.TrimSpace(entry)
			if filepath.IsAbs(entry) && filepath.Clean(entry) == cleaned {
				return refuseShells(cleaned)
			}
		}
		return "", ErrExecDenied
	}
	// Bare names authorize only against bare entries: an absolute allowlist
	// entry never grants its own base name.
	for _, entry := range c.Allowlist {
		entry = strings.TrimSpace(entry)
		if entry == name && !strings.ContainsAny(entry, "/\\") && !filepath.IsAbs(entry) {
			path, err := lookupInSandboxPath(name)
			if err != nil {
				return "", err
			}
			return refuseShells(path)
		}
	}
	return "", ErrExecDenied
}

// refuseShells rejects a resolved path when the target — or whatever its symlink
// chain resolves to — is a known shell interpreter.
func refuseShells(path string) (string, error) {
	if isShellPath(path) {
		return "", fmt.Errorf("%w: shell interpreters are always refused (%s)", ErrExecDenied, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrExecUnresolved, path, err)
	}
	if isShellPath(resolved) {
		return "", fmt.Errorf("%w: shell interpreters are always refused (%s resolves to %s)", ErrExecDenied, path, resolved)
	}
	return path, nil
}

func isShellPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	_, ok := shellInterpreterNames[base]
	return ok
}

// lookupInSandboxPath resolves a bare name against the fixed child PATH instead
// of the gateway process's own PATH.
func lookupInSandboxPath(name string) (string, error) {
	for _, dir := range filepath.SplitList(execChildPATH) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("%w: %q", ErrExecNotFound, name)
}

// minimalExecEnvironment exposes only what ordinary CLIs need: the fixed sandbox
// PATH and the OS notion of home. Everything else — tokens, secrets, container
// plumbing — stays out of the child's view.
func minimalExecEnvironment() []string {
	env := []string{"PATH=" + execChildPATH}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		env = append(env, "HOME="+home)
	}
	if runtime.GOOS == "windows" {
		if root := os.Getenv("SystemRoot"); root != "" {
			env = append(env, "SystemRoot="+root)
		}
	}
	return env
}

type execBuffer struct {
	max      int
	b        []byte
	overflow bool
	cancel   context.CancelFunc
}

func (b *execBuffer) Write(p []byte) (int, error) {
	n := min(max(b.max-len(b.b), 0), len(p))
	if n > 0 {
		b.b = append(b.b, p[:n]...)
	}
	if n < len(p) {
		b.overflow = true
		b.cancel()
	}
	return len(p), nil
}

func validExecRequest(req abi.ExecRequest) bool {
	if req.Name == "" || len(req.Name) > abi.ExecMaxName || len(req.Args) > abi.ExecMaxArgs {
		return false
	}
	for _, arg := range req.Args {
		if len(arg) > abi.ExecMaxArg {
			return false
		}
	}
	return true
}

func registerExec(b wazero.HostModuleBuilder, commander Commander) wazero.HostModuleBuilder {
	return b.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, reqPtr, reqLen, outPtr, outCap int32) int32 {
			if commander == nil {
				return abi.ExecErrUnavailable
			}
			if reqLen <= 0 || outCap < 0 {
				return abi.ExecErrTooLarge
			}
			raw, ok := m.Memory().Read(uint32(reqPtr), uint32(reqLen))
			if !ok {
				return abi.ExecErrFailed
			}
			req, err := abi.DecodeExecRequest(raw)
			if err != nil || !validExecRequest(req) {
				return abi.ExecErrTooLarge
			}
			resp, err := commander.Run(ctx, req)
			switch {
			case errors.Is(err, ErrExecTooLarge):
				return abi.ExecErrTooLarge
			case errors.Is(err, ErrExecDenied), errors.Is(err, ErrExecNotFound), errors.Is(err, ErrExecUnresolved):
				return abi.ExecErrUnavailable
			case err != nil:
				return abi.ExecErrFailed
			}
			if len(resp.Stdout) > abi.ExecMaxOutput || len(resp.Stderr) > abi.ExecMaxOutput {
				return abi.ExecErrTooLarge
			}
			enc := abi.EncodeExecResponse(resp)
			n := int32(len(enc))
			if n > outCap {
				return n
			}
			if n > 0 && !m.Memory().Write(uint32(outPtr), enc) {
				return abi.ExecErrFailed
			}
			return n
		}).
		Export("exec")
}
