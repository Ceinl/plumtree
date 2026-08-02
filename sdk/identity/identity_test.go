package identity

import (
	"context"
	"errors"
	"testing"

	legacy "github.com/Ceinl/plumtree/sdk"
)

func TestUnavailableErrorMapping(t *testing.T) {
	if !errors.Is(normalize(legacy.ErrAuthUnavailable), ErrUnavailable) {
		t.Fatal("unavailable host error was not mapped to the package contract")
	}
}

func TestWhoamiNativeContract(t *testing.T) {
	result := Whoami().Run(context.Background())
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.User == "" || result.Kind == "" {
		t.Fatalf("identity = %#v", result.Identity)
	}
}
