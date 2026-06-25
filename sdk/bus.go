package sdk

import "errors"

// Pub/sub — live shared rooms. Where KV gives durable per-app state, the bus
// gives live notification: a message published to a topic is delivered as a
// MessageMsg to Model.Update of every session of the same app subscribed to
// that topic, with no polling. The two compose: keep the durable log in KV and
// publish a nudge (or the new item) over the bus so other sessions redraw
// immediately.
//
// Delivery is best-effort and at-most-once per session: if a session is not
// draining fast enough the host may drop a message rather than block the
// publisher, so the bus is a liveness signal, not the source of truth. Persist
// anything that must survive in KV.
//
// The same API works in `go run .` (a process-local bus) and in the hosted WASM
// sandbox (the host's bus capability shared across the app's sessions).

var (
	// ErrBusUnavailable means the running context provides no bus capability (or
	// the host reported an internal failure).
	ErrBusUnavailable = errors.New("sdk: bus capability unavailable")
	// ErrBusTooLarge means the topic or payload exceeds the host size limit.
	ErrBusTooLarge = errors.New("sdk: bus topic or payload too large")
)

// Subscribe registers the current session to receive MessageMsg events for
// topic. Subscribing more than once to the same topic is harmless. Call it
// early (e.g. before or at the start of the loop) so messages published by
// other sessions are delivered.
func Subscribe(topic string) error { return busSubscribe(topic) }

// Publish sends data to every session of this app subscribed to topic,
// delivered as a MessageMsg. The publisher's own session receives it too.
func Publish(topic string, data []byte) error { return busPublish(topic, data) }
