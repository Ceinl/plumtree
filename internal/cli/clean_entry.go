package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/internal/cli/paired"
	"github.com/Ceinl/plumtree/internal/cli/workflow"
	"github.com/Ceinl/plumtree/internal/selfupdate"
	"golang.org/x/term"
)

// DevRoot optionally points local project builds at a Plumtree checkout's SDK.
var DevRoot string

// RunClean is the selected root author workflow surface.
func RunClean(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "version":
			fmt.Fprintln(out, selfupdate.DescribeBuild(selfupdate.StampedVersion))
			return 0
		case "update":
			exePath, exeErr := os.Executable()
			if exeErr != nil {
				fmt.Fprintln(errOut, terminalSafeText("resolve the running binary: "+exeErr.Error()))
				return 1
			}
			command := selfupdate.Command{
				ExePath: exePath,
				Version: selfupdate.StampedVersion,
				Confirm: func(prompt string) bool { return interactiveConfirm(prompt, in, out) },
			}
			if err := command.Run(args[1:], out, errOut); err != nil {
				fmt.Fprintln(errOut, terminalSafeText(err.Error()))
				return 1
			}
			return 0
		}
	}
	printUpdateNotice(args, out, errOut)
	storePath, err := paired.DefaultPath()
	if err != nil {
		fmt.Fprintln(errOut, terminalSafeText(err.Error()))
		return 1
	}
	keyDir := filepath.Join(filepath.Dir(storePath), "keys")
	runner := workflow.Runner{In: in, Out: out, Err: errOut, StorePath: storePath, KeyDir: keyDir, Workspace: DevRoot,
		Open: func(ctx context.Context, record paired.ServerRecord) (*workflow.API, error) {
			connection, err := paired.DialControl(ctx, record, paired.DialConfig{KeyStore: paired.FileKeyStore{Dir: keyDir}, Timeout: 15 * time.Second})
			if err != nil {
				return nil, err
			}
			return workflow.NewAPI(connection)
		},
		Confirm: func(prompt string) bool { return interactiveConfirm(prompt, in, out) },
	}
	if err := runner.Run(args); err != nil {
		fmt.Fprintln(errOut, terminalSafeText(err.Error()))
		return 1
	}
	return 0
}

// printUpdateNotice reports the passive update hint on interactive terminals
// and kicks off the opportunistic daily refresh, both without ever holding up
// the command.
func printUpdateNotice(args []string, out, errOut io.Writer) {
	if os.Getenv(selfupdate.OptOutEnv) != "" || !isInteractiveStdin() {
		return
	}
	current := selfupdate.StampedVersion
	selfupdate.RefreshNoticeCache(os.UserCacheDir, current, time.Now())
	if len(args) > 0 {
		return // bare pt stays quiet so the usage line is never polluted
	}
	if hint := selfupdate.PassiveHint(os.UserCacheDir, current, time.Now()); hint != "" {
		fmt.Fprintln(errOut, hint)
	}
}

func isInteractiveStdin() bool {
	file, ok := any(os.Stdin).(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func interactiveConfirm(prompt string, in io.Reader, out io.Writer) bool {
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
