//go:build !wasip1

package bus

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
)

func TestPublishCopiesAndDeliversNativeMessage(t *testing.T) {
	topic := "typed-bus-test"
	inbox := make(chan Message, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go messageSource(topic, func(message Message) app.Event {
		inbox <- message
		return nil
	})(ctx, func(app.Event) {})
	// Let the source register before publishing.
	ready := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		native.Lock()
		ready = len(native.topics[topic]) > 0
		native.Unlock()
		if ready {
			break
		}
		runtime.Gosched()
	}
	if !ready {
		t.Fatal("subscription did not register")
	}
	data := []byte("payload")
	if err := publish(topic, data); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	select {
	case message := <-inbox:
		if string(message.Data) != "payload" {
			t.Fatalf("message data = %q", message.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("message was not delivered")
	}
}
