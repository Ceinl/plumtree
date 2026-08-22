package cleanrole

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/internal/runner"
	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
)

func TestRunnerRoleServesIsolatedWorkerOverAuthenticatedUnixSocket(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "runner-worker")
	command := exec.Command("go", "build", "-o", worker, "./cmd/runner-worker")
	command.Dir = filepath.Join("..", "..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build runner worker: %v\n%s", err, output)
	}
	tokenPath := filepath.Join(dir, "runner.token")
	if err := os.WriteFile(tokenPath, []byte("isolated-runner-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("/tmp", "pt-r-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	endpoint := "unix://" + filepath.Join(socketDir, "runner.sock")
	cfg := serverconfig.Default()
	cfg.Roles = serverconfig.Roles{Runner: true}
	cfg.Runtime.RunnerEndpoint = endpoint
	cfg.Runtime.RunnerWorker = worker
	cfg.Runtime.RunnerScratchRoot = filepath.Join(dir, "scratch")
	cfg.Secrets.RunnerTokenFile = tokenPath
	projection, err := serverconfig.MaterializeRole(cfg, serverconfig.RoleRunner)
	if err != nil {
		t.Fatal(err)
	}
	var brokerLog bytes.Buffer
	component := &runnerComponent{projection: projection, out: &brokerLog}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := component.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := component.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	process := runner.NewRemoteProcessRunner(endpoint, "isolated-runner-token")
	err = process.RunCLI(context.Background(), buildCleanCLI(t), runner.DefaultLimits,
		runner.Capabilities{Auth: runner.StaticAuth{Identity: runner.Identity{User: "owner", Kind: runner.IdentitySSHKey, Authenticated: true}}},
		[]string{"get_identity"}, &output)
	if err != nil || !strings.Contains(output.String(), "owner authenticated=true") {
		t.Fatalf("isolated output=%q err=%v broker=%q", output.String(), err, brokerLog.String())
	}
	cancel()
	if err := component.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
