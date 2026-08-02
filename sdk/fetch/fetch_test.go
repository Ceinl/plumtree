package fetch

import (
	"context"
	"errors"
	"strings"
	"testing"

	legacy "github.com/Ceinl/plumtree/sdk"
)

func TestUnavailableErrorMapping(t *testing.T) {
	if !errors.Is(normalize(legacy.ErrFetchUnavailable), ErrUnavailable) {
		t.Fatal("unavailable host error was not mapped to the package contract")
	}
}

func TestRequestValidationAndCancellation(t *testing.T) {
	if result := Request("GET", "", nil).Run(context.Background()); !errors.Is(result.Err, ErrInvalid) {
		t.Fatalf("empty URL err = %v", result.Err)
	}
	if result := Get(strings.Repeat("x", 2049)).Run(context.Background()); !errors.Is(result.Err, ErrTooLarge) {
		t.Fatalf("large URL err = %v", result.Err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := Get("http://127.0.0.1").Run(ctx); !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("cancelled err = %v", result.Err)
	}
}
