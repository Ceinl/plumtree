package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

func TestSuspensionHubDrainsBusyGatewayBeforeCancellationAcknowledgement(t *testing.T) {
	hub := newSuspensionHub()
	if err := hub.register("gateway-1"); err != nil {
		t.Fatal(err)
	}

	const deliveries = 9 // one more than the gateway's bounded event queue
	results := make(chan error, deliveries)
	for range deliveries {
		go func() {
			results <- hub.publish(control.SuspensionEvent{Scope: control.SuspensionApp, ID: "app-1"})
		}()
	}
	// Let one publisher fill the queue. A further publisher must not keep the
	// hub lock while it waits, since next and ack need that lock to drain it.
	time.Sleep(50 * time.Millisecond)

	for range deliveries {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		delivery, ok, err := hub.next(ctx, "gateway-1")
		cancel()
		if err != nil || !ok {
			t.Fatalf("next suspension = (%+v, %v, %v), want delivery", delivery, ok, err)
		}
		if err := hub.ack("gateway-1", delivery.id); err != nil {
			t.Fatal(err)
		}
	}
	for range deliveries {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("publish did not wait for the gateway cancellation acknowledgement")
		}
	}
}
