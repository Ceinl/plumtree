package timer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/ui"
)

func TestAfterAndCancellation(t *testing.T) {
	if result := After(0).Run(context.Background()); result.Err != nil {
		t.Fatal(result.Err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := After(0).Run(ctx); !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("zero delay with cancelled context err = %v", result.Err)
	}
	if result := After(time.Hour).Run(ctx); !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("cancelled err = %v", result.Err)
	}
	if result := After(-time.Second).Run(context.Background()); !errors.Is(result.Err, ErrInvalid) {
		t.Fatalf("negative delay err = %v", result.Err)
	}
}

func TestEveryReportsInvalidIntervalToRuntime(t *testing.T) {
	runtime := app.NewRuntime(&invalidEveryModel{})
	if err := runtime.Init(context.Background()); err == nil {
		t.Fatal("expected invalid interval error")
	}
	if runtime.Err() == nil {
		t.Fatal("expected stored runtime error")
	}
}

type invalidEveryModel struct{}

func (*invalidEveryModel) Update(app.Event) app.Command { return app.Noop() }
func (*invalidEveryModel) View() ui.Node                { return ui.Text("timer") }
func (*invalidEveryModel) Subscriptions() app.Subscription {
	return Every("invalid", 0, nil)
}
