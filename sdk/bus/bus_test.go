package bus

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
)

func TestUnavailableErrorMapping(t *testing.T) {
	if !errors.Is(normalize(ErrUnavailable), ErrUnavailable) {
		t.Fatal("unavailable host error was not mapped to the package contract")
	}
}

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
	case <-context.Background().Done():
		t.Fatal("unreachable")
	}
}

func TestValidation(t *testing.T) {
	if result := Publish("", nil).Run(context.Background()); !errors.Is(result.Err, ErrInvalid) {
		t.Fatalf("empty topic err = %v", result.Err)
	}
}
