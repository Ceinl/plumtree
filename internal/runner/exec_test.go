package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[len(os.Args)-2] == "--exec-helper" {
		runExecHelper(os.Args[len(os.Args)-1])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runExecHelper lets the test binary re-exec itself as an allowlisted child,
// since LocalCommander's minimal environment cannot carry helper selection.
func runExecHelper(mode string) {
	switch mode {
	case "streams":
		fmt.Print("stdout")
		fmt.Fprint(os.Stderr, "stderr")
		os.Exit(7)
	case "flood":
		chunk := make([]byte, 64<<10)
		for range 20 {
			fmt.Print(chunk)
			fmt.Fprint(os.Stderr, chunk)
		}
	case "hang":
		time.Sleep(time.Hour)
	case "env":
		fmt.Printf("PATH=%s\nHOME=%s\nCANARY=%s\n", os.Getenv("PATH"), os.Getenv("HOME"), os.Getenv("PLUMTREE_EXEC_CANARY"))
	case "cwd":
		dir, _ := os.Getwd()
		fmt.Print(dir)
	}
}

func TestLocalCommanderCapturesOutputAndExitCode(t *testing.T) {
	cmd := LocalCommander{Allowlist: []string{os.Args[0]}, Timeout: time.Second}
	resp, err := cmd.Run(context.Background(), abi.ExecRequest{
		Name: os.Args[0], Args: []string{"-test.run=^$", "--", "--exec-helper", "streams"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ExitCode != 7 || string(resp.Stdout) != "stdout" || string(resp.Stderr) != "stderr" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestLocalCommanderEmptyAllowlistDeniesEverything(t *testing.T) {
	cmd := LocalCommander{}
	if _, err := cmd.Run(context.Background(), abi.ExecRequest{Name: "echo"}); !errors.Is(err, ErrExecDenied) {
		t.Fatalf("empty allowlist err = %v, want ErrExecDenied", err)
	}
	if _, err := cmd.Run(context.Background(), abi.ExecRequest{Name: "/bin/echo"}); !errors.Is(err, ErrExecDenied) {
		t.Fatalf("empty allowlist absolute-path err = %v, want ErrExecDenied", err)
	}
}

func TestLocalCommanderBareEntryRejectsPathRequests(t *testing.T) {
	cmd := LocalCommander{Allowlist: []string{"echo"}}
	for _, name := range []string{"./echo", "/bin/echo", "sub/dir/echo", "..\\echo"} {
		if _, err := cmd.Run(context.Background(), abi.ExecRequest{Name: name}); !errors.Is(err, ErrExecDenied) {
			t.Fatalf("%q err = %v, want ErrExecDenied", name, err)
		}
	}
}

func TestLocalCommanderAbsoluteEntryDoesNotAuthorizeBareName(t *testing.T) {
	cmd := LocalCommander{Allowlist: []string{"/bin/echo"}}
	if _, err := cmd.Run(context.Background(), abi.ExecRequest{Name: "echo"}); !errors.Is(err, ErrExecDenied) {
		t.Fatalf("bare-name err = %v, want ErrExecDenied", err)
	}
	if _, err := cmd.Run(context.Background(), abi.ExecRequest{Name: filepath.Join(t.TempDir(), "echo")}); !errors.Is(err, ErrExecDenied) {
		t.Fatalf("unlisted absolute path err = %v, want ErrExecDenied", err)
	}
}

func TestLocalCommanderRefusesShellsByNameAndPath(t *testing.T) {
	for _, entry := range []string{"sh", "/bin/sh", "/bin/bash"} {
		cmd := LocalCommander{Allowlist: []string{entry}}
		if _, err := cmd.Run(context.Background(), abi.ExecRequest{Name: entry}); !errors.Is(err, ErrExecDenied) {
			t.Fatalf("allowlisted shell %q err = %v, want ErrExecDenied", entry, err)
		}
	}
	link := filepath.Join(t.TempDir(), "myshell")
	if err := os.Symlink("/bin/sh", link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cmd := LocalCommander{Allowlist: []string{link}}
	if _, err := cmd.Run(context.Background(), abi.ExecRequest{Name: link}); !errors.Is(err, ErrExecDenied) {
		t.Fatalf("symlinked shell err = %v, want ErrExecDenied", err)
	}
}

func TestLocalCommanderRunsAuthorizedBareNameInFreshWorkDir(t *testing.T) {
	t.Setenv("PLUMTREE_EXEC_CANARY", "operator-secret")
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := LocalCommander{Allowlist: []string{os.Args[0]}, Timeout: time.Second}
	resp, err := cmd.Run(context.Background(), abi.ExecRequest{
		Name: os.Args[0], Args: []string{"-test.run=^$", "--", "--exec-helper", "cwd"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := string(resp.Stdout)
	if dir == before || !strings.Contains(dir, "plumtree-exec-") {
		t.Fatalf("child ran in %q, want a fresh plumtree-exec temp dir (gateway cwd %q)", dir, before)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("fresh work dir %q survived the exec", dir)
	}
}

func TestLocalCommanderMinimalEnvironmentNeverLeaksCanary(t *testing.T) {
	t.Setenv("PLUMTREE_EXEC_CANARY", "operator-secret")
	cmd := LocalCommander{Allowlist: []string{os.Args[0]}, Timeout: time.Second}
	resp, err := cmd.Run(context.Background(), abi.ExecRequest{
		Name: os.Args[0], Args: []string{"-test.run=^$", "--", "--exec-helper", "env"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(resp.Stdout)
	if strings.Contains(out, "operator-secret") {
		t.Fatalf("canary leaked into child environment:\n%s", out)
	}
	if !strings.Contains(out, "PATH=/usr/bin:/bin:/usr/sbin:/sbin") || !strings.Contains(out, "HOME=") {
		t.Fatalf("child environment missing minimal PATH/HOME:\n%s", out)
	}
}

func TestLocalCommanderWorkDirOverride(t *testing.T) {
	override := t.TempDir()
	resolved, err := filepath.EvalSymlinks(override)
	if err != nil {
		t.Fatal(err)
	}
	cmd := LocalCommander{Allowlist: []string{os.Args[0]}, WorkDir: override, Timeout: time.Second}
	resp, err := cmd.Run(context.Background(), abi.ExecRequest{
		Name: os.Args[0], Args: []string{"-test.run=^$", "--", "--exec-helper", "cwd"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Stdout) != resolved {
		t.Fatalf("child ran in %q, want operator override %q", resp.Stdout, resolved)
	}
}

func TestLocalCommanderRejectsOversizedOutput(t *testing.T) {
	cmd := LocalCommander{Allowlist: []string{os.Args[0]}}
	_, err := cmd.Run(context.Background(), abi.ExecRequest{
		Name: os.Args[0], Args: []string{"-test.run=^$", "--", "--exec-helper", "flood"},
	})
	if !errors.Is(err, ErrExecTooLarge) {
		t.Fatalf("error = %v, want output limit", err)
	}
}

func TestLocalCommanderEnforcesPerExecTimeout(t *testing.T) {
	cmd := LocalCommander{Allowlist: []string{os.Args[0]}, Timeout: 200 * time.Millisecond}
	started := time.Now()
	_, err := cmd.Run(context.Background(), abi.ExecRequest{
		Name: os.Args[0], Args: []string{"-test.run=^$", "--", "--exec-helper", "hang"},
	})
	if !errors.Is(err, ErrExecTimedOut) {
		t.Fatalf("error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("timeout took %s to fire", elapsed)
	}
}
