package timer

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAfterAndCancellation(t *testing.T) {
	if result := After(0).Run(context.Background()); result.Err != nil {
		t.Fatal(result.Err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := After(time.Hour).Run(ctx); !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("cancelled err = %v", result.Err)
	}
	if result := After(-time.Second).Run(context.Background()); !errors.Is(result.Err, ErrInvalid) {
		t.Fatalf("negative delay err = %v", result.Err)
	}
}
