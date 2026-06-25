package runner

import (
	"context"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/Ceinl/plumtree/sdk/abi"
)

// Bus is per-app scoped pub/sub, handed to a guest as the bus capability.
// Sessions of the same app share one Bus, so a message published by one session
// is delivered to every other session subscribed to the topic. Implementations
// must be safe for concurrent use.
//
// Where a Store gives durable shared state, a Bus gives live notification: it
// holds no history and makes no delivery guarantee. A message to a session that
// is not draining fast enough is dropped rather than blocking the publisher, so
// apps must keep anything durable in a Store.
type Bus interface {
	// Open returns a new per-session subscription. The caller must Close it when
	// the session ends.
	Open() Subscriber
	// Publish delivers data to every open subscription registered for topic. It
	// returns the number of subscriptions reached.
	Publish(topic string, data []byte) int
}

// Subscriber is one session's handle on a Bus: it registers topics and exposes
// the stream of incoming messages. *Subscription is the in-process
// implementation; a cross-process runner supplies a proxy that forwards to the
// parent. Keeping it an interface is what lets the bus span a process boundary.
type Subscriber interface {
	Subscribe(topic string)
	Events() <-chan abi.Event
	Close()
}

// busInboxSize bounds how many undelivered messages a single session may queue
// before the host starts dropping (best-effort delivery).
const busInboxSize = 64

// Subscription is one session's view of a Bus: the set of topics it listens to
// and a bounded inbox of pending messages, each already encoded as a KindMessage
// abi.Event ready for the recv loop. A Source that implements BusBinder selects
// on Events so an idle, blocked-in-recv session wakes the moment another session
// publishes.
type Subscription struct {
	bus    *MemBus
	events chan abi.Event

	mu     sync.Mutex
	topics map[string]bool
	closed bool
}

// Subscribe registers this subscription to receive messages published to topic.
// Subscribing to the same topic twice is harmless.
func (s *Subscription) Subscribe(topic string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.topics[topic] {
		s.mu.Unlock()
		return
	}
	s.topics[topic] = true
	s.mu.Unlock()
	s.bus.register(topic, s)
}

// Events is the channel of pending messages, drained by the session's Source.
func (s *Subscription) Events() <-chan abi.Event { return s.events }

// Close unregisters the subscription from every topic and stops delivery.
func (s *Subscription) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	topics := make([]string, 0, len(s.topics))
	for t := range s.topics {
		topics = append(topics, t)
	}
	s.mu.Unlock()
	for _, t := range topics {
		s.bus.unregister(t, s)
	}
}

// deliver queues a message for this subscription, dropping it if the inbox is
// full (best-effort delivery — the publisher is never blocked).
func (s *Subscription) deliver(topic string, data []byte) bool {
	ev := abi.Event{Kind: abi.KindMessage, Topic: topic, Data: data}
	select {
	case s.events <- ev:
		return true
	default:
		return false
	}
}

// MemBus is an in-memory Bus. It is the per-app pub/sub used by `pt dev` and the
// SSH gateway: one MemBus per app, shared across that app's live sessions.
type MemBus struct {
	mu   sync.Mutex
	subs map[string]map[*Subscription]struct{}
}

// NewMemBus returns an empty bus.
func NewMemBus() *MemBus {
	return &MemBus{subs: make(map[string]map[*Subscription]struct{})}
}

// Open returns a fresh subscription bound to this bus.
func (b *MemBus) Open() Subscriber {
	return &Subscription{
		bus:    b,
		events: make(chan abi.Event, busInboxSize),
		topics: make(map[string]bool),
	}
}

// Publish delivers data to every subscription registered for topic. Each
// subscriber gets its own copy of the payload.
func (b *MemBus) Publish(topic string, data []byte) int {
	b.mu.Lock()
	set := b.subs[topic]
	targets := make([]*Subscription, 0, len(set))
	for s := range set {
		targets = append(targets, s)
	}
	b.mu.Unlock()

	n := 0
	for _, s := range targets {
		// Copy per subscriber: the guest's payload buffer is reused after the
		// host call returns, and subscriptions may outlive it.
		if s.deliver(topic, append([]byte(nil), data...)) {
			n++
		}
	}
	return n
}

func (b *MemBus) register(topic string, s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	set := b.subs[topic]
	if set == nil {
		set = make(map[*Subscription]struct{})
		b.subs[topic] = set
	}
	set[s] = struct{}{}
}

func (b *MemBus) unregister(topic string, s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if set := b.subs[topic]; set != nil {
		delete(set, s)
		if len(set) == 0 {
			delete(b.subs, topic)
		}
	}
}

// BusBinder is implemented by a Source that can also deliver bus messages. When
// the app has a Bus capability, the runner hands the session's subscription
// channel to the Source so it can select on input and incoming messages
// together.
type BusBinder interface {
	BindBus(events <-chan abi.Event)
}

// registerBus adds the bus_sub/bus_pub host functions to b. They are installed
// even when bus is nil so a guest whose linker kept the imports can still
// instantiate; calls then return abi.BusErrInternal. Size caps are enforced
// here, before the Bus, so a hostile guest cannot exceed them.
func registerBus(b wazero.HostModuleBuilder, bus Bus, sub Subscriber) wazero.HostModuleBuilder {
	readTopic := func(m api.Module, ptr, length int32) (string, int32) {
		if length <= 0 || length > abi.BusMaxTopic {
			return "", abi.BusErrTooLarge
		}
		raw, ok := m.Memory().Read(uint32(ptr), uint32(length))
		if !ok {
			return "", abi.BusErrInternal
		}
		return string(raw), abi.BusOk
	}

	b = b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, topicPtr, topicLen int32) int32 {
			if sub == nil {
				return abi.BusErrInternal
			}
			topic, code := readTopic(m, topicPtr, topicLen)
			if topic == "" {
				return code
			}
			sub.Subscribe(topic)
			return abi.BusOk
		}).
		Export("bus_sub")

	b = b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, topicPtr, topicLen, dataPtr, dataLen int32) int32 {
			if bus == nil {
				return abi.BusErrInternal
			}
			topic, code := readTopic(m, topicPtr, topicLen)
			if topic == "" {
				return code
			}
			if dataLen < 0 || dataLen > abi.BusMaxData {
				return abi.BusErrTooLarge
			}
			raw, ok := m.Memory().Read(uint32(dataPtr), uint32(dataLen))
			if !ok {
				return abi.BusErrInternal
			}
			// Publish copies per subscriber; raw may alias guest memory.
			return int32(bus.Publish(topic, raw))
		}).
		Export("bus_pub")

	return b
}
