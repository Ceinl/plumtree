package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestLocalCommanderCapturesOutputAndExitCode(t *testing.T) {
	resp, err := (LocalCommander{}).Run(context.Background(), abi.ExecRequest{
		Name: "sh", Args: []string{"-c", "printf stdout; printf stderr >&2; exit 7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ExitCode != 7 || string(resp.Stdout) != "stdout" || string(resp.Stderr) != "stderr" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestLocalCommanderRejectsOversizedOutput(t *testing.T) {
	_, err := (LocalCommander{}).Run(context.Background(), abi.ExecRequest{
		Name: "sh", Args: []string{"-c", "yes x | head -c 1048577"},
	})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %v, want output limit", err)
	}
}
