package secrets

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	legacy "github.com/Ceinl/plumtree/sdk"
)

func TestUnavailableErrorMapping(t *testing.T) {
	if !errors.Is(normalize(legacy.ErrEnvUnavailable), ErrUnavailable) {
		t.Fatal("unavailable host error was not mapped to the package contract")
	}
}

func TestGetReadsNativeEnvironmentAndCopiesContract(t *testing.T) {
	t.Setenv("PLUMTREE_TYPED_SECRET", "value")
	result := Get("PLUMTREE_TYPED_SECRET").Run(context.Background())
	if result.Err != nil || !result.Found || result.Value != "value" {
		t.Fatalf("result = %#v", result)
	}
	if result := Get("PLUMTREE_MISSING_TYPED_SECRET").Run(context.Background()); result.Err != nil || result.Found {
		t.Fatalf("missing result = %#v", result)
	}
	_ = os.Unsetenv("PLUMTREE_TYPED_SECRET")
}

func TestValidation(t *testing.T) {
	if result := Get("").Run(context.Background()); !errors.Is(result.Err, ErrInvalid) {
		t.Fatalf("empty key err = %v", result.Err)
	}
	if result := Get(strings.Repeat("x", 257)).Run(context.Background()); !errors.Is(result.Err, ErrTooLarge) {
		t.Fatalf("large key err = %v", result.Err)
	}
}
