// Command codex-ssh exposes one owner-only `codex exec` invocation over SSH.
// It demonstrates the high-trust host-command capability without becoming a
// general-purpose remote shell.
package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/Ceinl/plumtree/sdk"
)

const maxPromptBytes = 32 * 1024

func codexArgs(prompt, workdir, sandbox string) ([]string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, errors.New("usage: ssh <owner>/codex-ssh@<host> 'your prompt'")
	}
	if len(prompt) > maxPromptBytes {
		return nil, fmt.Errorf("prompt exceeds %d bytes", maxPromptBytes)
	}
	if sandbox == "" {
		sandbox = "read-only"
	}
	if sandbox != "read-only" && sandbox != "workspace-write" {
		return nil, errors.New("CODEX_SANDBOX must be read-only or workspace-write")
	}
	args := []string{"exec", "--color", "never", "--ephemeral", "--sandbox", sandbox}
	if workdir != "" {
		args = append(args, "-C", workdir)
	}
	return append(args, prompt), nil
}

func optionalEnv(key string) (string, error) {
	value, _, err := sdk.Env(key)
	if errors.Is(err, sdk.ErrEnvUnavailable) {
		return "", nil
	}
	return value, err
}

func callerAllowed(identity sdk.Identity, allowedFingerprints string) bool {
	if identity.Kind != sdk.IdentitySSHKey {
		return false
	}
	if identity.Authenticated && identity.OwnsApp {
		return true
	}
	for _, fingerprint := range strings.FieldsFunc(allowedFingerprints, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	}) {
		if fingerprint == identity.User {
			return true
		}
	}
	return false
}

func main() {
	sdk.CLI(sdk.Meta{Name: "codex-ssh", Type: "cli"}, func(ctx sdk.Ctx, sshArgs []string) error {
		identity, err := sdk.Whoami()
		if err != nil {
			return fmt.Errorf("verify caller: %w", err)
		}
		allowedFingerprints, err := optionalEnv("CODEX_ALLOWED_SSH_KEYS")
		if err != nil {
			return fmt.Errorf("read CODEX_ALLOWED_SSH_KEYS: %w", err)
		}
		if !callerAllowed(identity, allowedFingerprints) {
			return fmt.Errorf("access denied for %s; add this fingerprint to CODEX_ALLOWED_SSH_KEYS", identity.User)
		}

		workdir, err := optionalEnv("CODEX_WORKDIR")
		if err != nil {
			return fmt.Errorf("read CODEX_WORKDIR: %w", err)
		}
		sandbox, err := optionalEnv("CODEX_SANDBOX")
		if err != nil {
			return fmt.Errorf("read CODEX_SANDBOX: %w", err)
		}
		args, err := codexArgs(strings.Join(sshArgs, " "), workdir, sandbox)
		if err != nil {
			return err
		}
		result, err := sdk.Exec("codex", args...)
		if err != nil {
			if errors.Is(err, sdk.ErrExecUnavailable) {
				return errors.New("Codex bridge unavailable: claim the app and enable allowHostCommands")
			}
			return fmt.Errorf("start codex: %w", err)
		}
		if len(result.Stderr) > 0 {
			ctx.Out().Print(string(result.Stderr))
		}
		if len(result.Stdout) > 0 {
			ctx.Out().Print(string(result.Stdout))
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("codex exited with status %d", result.ExitCode)
		}
		return nil
	})
}
