// Command codex-ssh exposes one owner-only `codex exec` invocation over SSH.
// It demonstrates the high-trust host-command capability without becoming a
// general-purpose remote shell.
package main

import (
	"errors"
	"fmt"
	"strings"

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

func main() {
	sdk.CLI(sdk.Meta{Name: "codex-ssh", Type: "cli"}, func(ctx sdk.Ctx, sshArgs []string) error {
		identity, err := sdk.Whoami()
		if err != nil {
			return fmt.Errorf("verify caller: %w", err)
		}
		if !identity.Authenticated || !identity.OwnsApp {
			return errors.New("access denied: authenticate with the claimed owner's SSH key")
		}

		workdir, _, err := sdk.Env("CODEX_WORKDIR")
		if err != nil {
			return fmt.Errorf("read CODEX_WORKDIR: %w", err)
		}
		sandbox, _, err := sdk.Env("CODEX_SANDBOX")
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
