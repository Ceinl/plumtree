package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/Ceinl/plumtree/internal/cli/paired"
	"github.com/Ceinl/plumtree/internal/cli/workflow"
)

// DevRoot optionally points local project builds at a Plumtree checkout's SDK.
var DevRoot string

// RunClean is the selected root author workflow surface.
func RunClean(args []string, in io.Reader, out, errOut io.Writer) int {
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
		Confirm: func(string) bool { return false },
	}
	if err := runner.Run(args); err != nil {
		fmt.Fprintln(errOut, terminalSafeText(err.Error()))
		return 1
	}
	return 0
}
