// Package bus provides typed live topic operations and app subscriptions.
// Authority is the app-scoped topic capability; messages live only until
// delivery and are best-effort notifications, so durable state belongs in kv.
// Native uses an isolated process-local adapter. Hosted apps use the clean ABI
// event contract. The package owns topic validation and copies payloads at both
// boundaries.
package bus

import (
	"context"
	"errors"
	"fmt"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/internal/operation"
)

var (
	ErrUnavailable = errors.New("bus: capability unavailable")
	ErrInvalid     = errors.New("bus: invalid topic")
	ErrTooLarge    = errors.New("bus: topic or payload too large")
)

// Message is a copied topic notification. Err reports invalid subscription
// input or a selected adapter that cannot provide the subscription.
type Message struct {
	Topic string
	Data  []byte
	Err   error
}

type PublishResult struct{ Err error }

type PublishOperation struct {
	inner operation.Operation[PublishResult]
}

func (op PublishOperation) Run(ctx context.Context) PublishResult { return op.inner.Run(ctx) }
func (op PublishOperation) Map(mapper func(PublishResult) app.Event) app.Command {
	return op.inner.Map(mapper)
}
func (op PublishOperation) Ignore() app.Command { return op.inner.Ignore() }

// Publish creates an inert best-effort topic publish operation.
func Publish(topic string, data []byte) PublishOperation {
	dataCopy := append([]byte(nil), data...)
	return PublishOperation{inner: operation.New(func(ctx context.Context) PublishResult {
		if err := validate(topic, dataCopy); err != nil {
			return PublishResult{Err: err}
		}
		if err := ctx.Err(); err != nil {
			return PublishResult{Err: err}
		}
		return PublishResult{Err: publish(topic, dataCopy)}
	})}
}

// Messages declares a stable live subscription. The mapper runs in the app
// event queue, preserving serialized model updates.
func Messages(key app.SubscriptionKey, topic string, mapper func(Message) app.Event) app.Subscription {
	definition := fmt.Sprintf("bus:%s", topic)
	if err := validate(topic, nil); err != nil {
		return app.Source(key, definition, func(ctx context.Context, emit func(app.Event)) {
			if mapper == nil {
				return
			}
			event := mapper(Message{Topic: topic, Err: err})
			select {
			case <-ctx.Done():
				return
			default:
				emit(event)
			}
		})
	}
	registerTopic(topic)
	subscription := app.Source(key, definition, messageSource(topic, mapper))
	if mapper == nil {
		return subscription
	}
	return app.Subscription{{
		Key: key, Definition: definition, Start: messageSource(topic, mapper),
		Filter: func(event app.Event) (app.Event, bool) {
			message, ok := event.(app.MessageEvent)
			if !ok || message.Topic != topic {
				return nil, false
			}
			return mapper(Message{Topic: message.Topic, Data: append([]byte(nil), message.Data...)}), true
		},
	}}
}

func validate(topic string, data []byte) error {
	if topic == "" {
		return ErrInvalid
	}
	if len(topic) > abi.BusMaxTopic || len(data) > abi.BusMaxData {
		return ErrTooLarge
	}
	return nil
}

func normalize(err error) error {
	if err == nil {
		return nil
	}
	return err
}
