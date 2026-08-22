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
	"golang.org/x/term"
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
		Confirm: func(prompt string) bool { return interactiveConfirm(prompt, in, out) },
	}
	if err := runner.Run(args); err != nil {
		fmt.Fprintln(errOut, terminalSafeText(err.Error()))
		return 1
	}
	return 0
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
