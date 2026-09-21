package selfupdate

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Command is the `update` command surface shared by pt and plumtree. Every
// dependency is a field, so tests inject a fake release source and a
// temporary directory standing in for the install directory.
type Command struct {
	ExePath string
	Version string
	Source  Source
	Confirm func(string) bool
	// Fault forces a mid-swap failure to observe rollback. Tests only; nil
	// in production.
	Fault func(Target) error
}

// HandlesCommand reports whether args[0] is the version or update subcommand
// this package dispatches.
func HandlesCommand(arg string) bool {
	return arg == "version" || arg == "update"
}

// RunCommand is the shared dispatch for the version/update subcommands both
// binaries expose. It returns nil on success, non-nil (with
// ErrNoConfirmation among the refusals) otherwise; callers map errors to
// their own exit code. base supplies the running-binary identity; a zero
// ExePath resolves the actual executable.
func RunCommand(base Command, args []string, out, errOut io.Writer) error {
	if len(args) == 0 || !HandlesCommand(args[0]) {
		return errNotACommand
	}
	if args[0] == "version" {
		_, _ = fmt.Fprintln(out, DescribeBuild(base.Version))
		return nil
	}
	exePath := base.ExePath
	if exePath == "" {
		resolved, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve the running binary: %w", err)
		}
		exePath = resolved
	}
	command := base
	command.ExePath = exePath
	return command.Run(args[1:], out, errOut)
}

var errNotACommand = errors.New("selfupdate: not a version/update command")

// InteractiveConfirm reads a y/N prompt on an interactive terminal only.
func InteractiveConfirm(in io.Reader, out io.Writer) func(string) bool {
	return func(prompt string) bool {
		file, ok := in.(*os.File)
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return false
		}
		_, _ = fmt.Fprintf(out, "%s [y/N] ", prompt)
		line, err := bufio.NewReader(io.LimitReader(in, 32)).ReadString('\n')
		if err != nil && len(line) == 0 {
			return false
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes"
	}
}
