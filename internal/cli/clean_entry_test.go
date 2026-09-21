package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCleanNonInteractiveConfirmationDoesNotBlock(t *testing.T) {
	t.Setenv("PLUMTREE_PT_SERVERS_FILE", t.TempDir()+"/servers.json")
	var stdout, stderr bytes.Buffer
	if code := RunClean([]string{"secret", "rm", "app", "KEY"}, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	if got := stderr.String(); !strings.Contains(got, "--yes") {
		t.Fatalf("stderr = %q", got)
	}
}
