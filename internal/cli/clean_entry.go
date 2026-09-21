package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		if selfupdate.HandlesCommand(args[0]) {
			command := selfupdate.Command{Version: selfupdate.StampedVersion, Confirm: selfupdate.InteractiveConfirm(in, out)}
			if err := selfupdate.RunCommand(command, args, out, errOut); err != nil {
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
		Confirm: selfupdate.InteractiveConfirm(in, out),
	}
	if err := runner.Run(args); err != nil {
		fmt.Fprintln(errOut, terminalSafeText(err.Error()))
		return 1
	}
	return 0
}

// printUpdateNotice reports the passive update hint on a bare interactive pt
// and refreshes the daily check. Bare pt refreshes synchronously (bounded by
// the same 2s budget) so short-lived invocations still populate the cache;
// real subcommands refresh asynchronously so the check never holds up work.
func printUpdateNotice(args []string, out, errOut io.Writer) {
	if os.Getenv(selfupdate.OptOutEnv) != "" || !isInteractiveStdin() {
		return
	}
	current := selfupdate.StampedVersion
	if len(args) == 0 {
		selfupdate.RefreshNoticeCacheSync(os.UserCacheDir, current)
	} else {
		selfupdate.RefreshNoticeCache(os.UserCacheDir, current, time.Now())
	}
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
