package app

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// SubscriptionKey is declaration identity. It is not a cancellation handle
// and is never delivered to Model.Update.
type SubscriptionKey string

// Subscription is a declarative collection of ongoing event sources.
type Subscription []SubscriptionSpec

// SubscriptionSpec describes one stable-key source. Definition is compared
// during reconciliation; changing it replaces the source.
type SubscriptionSpec struct {
	Key        SubscriptionKey
	Definition string
	Timer      *TimerSpec
	Start      func(context.Context, func(Event))
}

// TimerSpec is the deterministic timer source used by app.Every.
type TimerSpec struct {
	Interval time.Duration
	Event    Event
}

// Every declares a recurring event source. The runtime's virtual clock drives
// it in plumtest; host integrations may use the same definition with a real
// clock.
func Every(key SubscriptionKey, interval time.Duration, event Event) Subscription {
	return Subscription{{
		Key: key, Definition: fmt.Sprintf("every:%d", interval),
		Timer: &TimerSpec{Interval: interval, Event: event},
	}}
}

// Source declares a custom ongoing source. Its callback must stop when ctx is
// canceled and must not mutate the model directly; emitted values are queued
// through the serialized runtime.
func Source(key SubscriptionKey, definition string, start func(context.Context, func(Event))) Subscription {
	return Subscription{{Key: key, Definition: definition, Start: start}}
}

// Merge combines declarations and preserves declaration order.
func Merge(subscriptions ...Subscription) Subscription {
	var merged Subscription
	for _, subscription := range subscriptions {
		merged = append(merged, subscription...)
	}
	return merged
}

func (r *Runtime) reconcileLocked() {
	if r.stopped {
		return
	}
	declared := map[SubscriptionKey]SubscriptionSpec{}
	var declaredOrder []SubscriptionSpec
	if subscriber, ok := r.model.(Subscriber); ok {
		for _, spec := range subscriber.Subscriptions() {
			if spec.Key == "" {
				_ = r.failLocked(fmt.Errorf("app: subscription key is empty"))
				return
			}
			if _, exists := declared[spec.Key]; exists {
				_ = r.failLocked(fmt.Errorf("app: duplicate subscription key %q", spec.Key))
				return
			}
			if spec.Timer != nil && spec.Timer.Interval <= 0 {
				_ = r.failLocked(fmt.Errorf("app: subscription %q has invalid interval", spec.Key))
				return
			}
			declared[spec.Key] = spec
			declaredOrder = append(declaredOrder, spec)
		}
	}
	for key := range r.subs {
		if _, present := declared[key]; !present {
			if cancel := r.subCancel[key]; cancel != nil {
				cancel()
			}
			delete(r.subCancel, key)
			delete(r.subs, key)
		}
	}
	for _, spec := range declaredOrder {
		key := spec.Key
		previous, present := r.subs[key]
		if present && previous.Definition == spec.Definition {
			continue
		}
		if cancel := r.subCancel[key]; cancel != nil {
			cancel()
		}
		r.subs[key] = spec
		if spec.Start != nil || (r.realTime && spec.Timer != nil) {
			sourceContext, cancel := context.WithCancel(r.ctx)
			r.subCancel[key] = cancel
			start := spec.Start
			if spec.Timer != nil {
				timer := *spec.Timer
				start = func(ctx context.Context, emit func(Event)) {
					ticker := time.NewTicker(timer.Interval)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							emit(timer.Event)
						}
					}
				}
			}
			go start(sourceContext, func(event Event) {
				r.mu.Lock()
				defer r.mu.Unlock()
				if sourceContext.Err() == nil && !r.stopped {
					r.queue = append(r.queue, event)
					r.processQueueLocked()
				}
			})
		}
	}
}

type timeState struct{ elapsed time.Duration }

func (clock *timeState) advance(duration time.Duration, subscriptions map[SubscriptionKey]SubscriptionSpec, queue *[]Event) {
	if duration <= 0 {
		return
	}
	clock.elapsed += duration
	keys := make([]string, 0, len(subscriptions))
	for key := range subscriptions {
		keys = append(keys, string(key))
	}
	sort.Strings(keys)
	for _, rawKey := range keys {
		spec := subscriptions[SubscriptionKey(rawKey)]
		if spec.Timer == nil || spec.Timer.Interval <= 0 {
			continue
		}
		interval := spec.Timer.Interval
		before := clock.elapsed - duration
		first := before/interval + 1
		last := clock.elapsed / interval
		for tick := first; tick <= last; tick++ {
			*queue = append(*queue, spec.Timer.Event)
		}
	}
}
