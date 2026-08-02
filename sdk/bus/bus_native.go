//go:build !wasip1

package bus

import (
	"context"
	"sync"

	"github.com/Ceinl/plumtree/sdk/app"
)

var native = struct {
	sync.Mutex
	topics map[string]map[chan Message]struct{}
}{topics: make(map[string]map[chan Message]struct{})}

func publish(topic string, data []byte) error {
	if err := validate(topic, data); err != nil {
		return err
	}
	native.Lock()
	defer native.Unlock()
	for inbox := range native.topics[topic] {
		message := Message{Topic: topic, Data: append([]byte(nil), data...)}
		select {
		case inbox <- message:
		default:
			// Notifications are liveness hints and may be dropped when a model is slow.
		}
	}
	return nil
}

func messageSource(topic string, mapper func(Message) app.Event) func(context.Context, func(app.Event)) {
	return func(ctx context.Context, emit func(app.Event)) {
		if topic == "" || mapper == nil {
			return
		}
		inbox := make(chan Message, 32)
		native.Lock()
		if native.topics[topic] == nil {
			native.topics[topic] = make(map[chan Message]struct{})
		}
		native.topics[topic][inbox] = struct{}{}
		native.Unlock()
		defer func() {
			native.Lock()
			delete(native.topics[topic], inbox)
			if len(native.topics[topic]) == 0 {
				delete(native.topics, topic)
			}
			native.Unlock()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case message := <-inbox:
				emit(mapper(message))
			}
		}
	}
}
