//go:build wasip1

package bus

import (
	"context"

	legacy "github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/app"
)

// The clean subscription adapter is intentionally unavailable until the
// versioned host event contract is selected. This keeps the new package from
// silently mixing its event lifetime with the legacy root surface.
func publish(topic string, data []byte) error {
	if err := validate(topic, data); err != nil {
		return err
	}
	if err := legacy.Publish(topic, data); err != nil {
		return normalize(err)
	}
	return nil
}

func messageSource(topic string, mapper func(Message) app.Event) func(context.Context, func(app.Event)) {
	return func(ctx context.Context, emit func(app.Event)) {
		if topic == "" || mapper == nil {
			return
		}
		message := mapper(Message{Topic: topic, Err: ErrUnavailable})
		select {
		case <-ctx.Done():
			return
		default:
			emit(message)
		}
	}
}
