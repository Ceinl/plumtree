package hostexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	legacy "github.com/Ceinl/plumtree/sdk"
)

func TestUnavailableErrorMapping(t *testing.T) {
	if !errors.Is(normalize(legacy.ErrExecUnavailable), ErrUnavailable) {
		t.Fatal("unavailable host error was not mapped to the package contract")
	}
}

func TestRunNativeCommand(t *testing.T) {
	result := Run("sh", "-c", "printf typed-operation").Run(context.Background())
	if result.Err != nil || result.ExitCode != 0 || string(result.Stdout) != "typed-operation" {
		t.Fatalf("result = %#v", result)
	}
}

func TestValidation(t *testing.T) {
	if result := Run("").Run(context.Background()); !errors.Is(result.Err, ErrInvalid) {
		t.Fatalf("empty command err = %v", result.Err)
	}
}

func TestRunCancelsAfterCommandStarts(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan Result, 1)
	go func() {
		result <- Run(
			os.Args[0],
			"-test.run=^TestRunCancellationHelper$",
			"--",
			"--hostexec-cancel-helper",
			marker,
		).Run(ctx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case got := <-result:
		if !errors.Is(got.Err, context.Canceled) {
			t.Fatalf("cancelled command err = %v", got.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("command did not stop after cancellation")
	}
}

func TestRunCancellationHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--hostexec-cancel-helper" {
		return
	}
	if err := os.WriteFile(os.Args[len(os.Args)-1], []byte("started"), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Hour)
	}
}
