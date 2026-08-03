package hostexec

import (
	"context"
	"errors"
	"testing"
)

func TestUnavailableErrorMapping(t *testing.T) {
	if !errors.Is(normalize(ErrUnavailable), ErrUnavailable) {
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
