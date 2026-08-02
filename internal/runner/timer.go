package runner

import (
	"context"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/tetratelabs/wazero"
)

type timerService interface {
	Start(delay time.Duration, recurring bool) int32
	Cancel(id uint32) bool
	Events() <-chan abi.Event
	Close()
}

type hostTimer struct {
	stop chan struct{}
}

// timerManager owns the bounded set of timers for one session. It coalesces
// recurring ticks per timer, so a slow guest cannot create an unbounded queue.
type timerManager struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	next    uint32
	active  map[uint32]*hostTimer
	pending []uint32
	queued  map[uint32]bool
	signal  chan struct{}
	events  chan abi.Event
}

func newTimerManager(ctx context.Context) *timerManager {
	ctx, cancel := context.WithCancel(ctx)
	t := &timerManager{
		ctx:    ctx,
		cancel: cancel,
		active: make(map[uint32]*hostTimer),
		queued: make(map[uint32]bool),
		signal: make(chan struct{}, 1),
		events: make(chan abi.Event),
	}
	go t.dispatch()
	return t
}

func (t *timerManager) Start(delay time.Duration, recurring bool) int32 {
	minimum := time.Duration(abi.TimerMinDelayNanos)
	if recurring {
		minimum = time.Duration(abi.TimerMinEveryNanos)
	}
	if delay < minimum || delay > time.Duration(abi.TimerMaxDelayNanos) {
		return abi.TimerErrInvalid
	}

	t.mu.Lock()
	if len(t.active) >= abi.TimerMaxActive {
		t.mu.Unlock()
		return abi.TimerErrLimit
	}
	for {
		t.next++
		if t.next != 0 && t.next <= uint32(^uint32(0)>>1) {
			if _, exists := t.active[t.next]; !exists {
				break
			}
		}
		if t.next > uint32(^uint32(0)>>1) {
			t.next = 0
		}
	}
	id := t.next
	timer := &hostTimer{stop: make(chan struct{})}
	t.active[id] = timer
	t.mu.Unlock()

	go t.run(id, timer, delay, recurring)
	return int32(id)
}

func (t *timerManager) run(id uint32, timer *hostTimer, delay time.Duration, recurring bool) {
	if recurring {
		ticker := time.NewTicker(delay)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.enqueue(id, true)
			case <-timer.stop:
				return
			case <-t.ctx.Done():
				return
			}
		}
	}

	clock := time.NewTimer(delay)
	defer clock.Stop()
	select {
	case <-clock.C:
		t.enqueue(id, false)
	case <-timer.stop:
	case <-t.ctx.Done():
	}
}

func (t *timerManager) enqueue(id uint32, recurring bool) {
	t.mu.Lock()
	if _, active := t.active[id]; !active {
		t.mu.Unlock()
		return
	}
	if !recurring {
		delete(t.active, id)
	}
	if !t.queued[id] {
		t.queued[id] = true
		t.pending = append(t.pending, id)
	}
	t.mu.Unlock()
	select {
	case t.signal <- struct{}{}:
	default:
	}
}

func (t *timerManager) dispatch() {
	defer close(t.events)
	for {
		t.mu.Lock()
		var id uint32
		if len(t.pending) > 0 {
			id = t.pending[0]
		}
		t.mu.Unlock()

		if id == 0 {
			select {
			case <-t.signal:
				continue
			case <-t.ctx.Done():
				return
			}
		}

		select {
		case t.events <- abi.Event{Kind: abi.KindTimer, CommandID: id}:
			t.mu.Lock()
			if len(t.pending) > 0 && t.pending[0] == id {
				t.pending = t.pending[1:]
				delete(t.queued, id)
			}
			t.mu.Unlock()
		case <-t.ctx.Done():
			return
		}
	}
}

func (t *timerManager) Cancel(id uint32) bool {
	t.mu.Lock()
	timer, active := t.active[id]
	if active {
		delete(t.active, id)
		close(timer.stop)
	}
	delete(t.queued, id)
	for i, pending := range t.pending {
		if pending == id {
			t.pending = append(t.pending[:i], t.pending[i+1:]...)
			break
		}
	}
	t.mu.Unlock()
	return active
}

func (t *timerManager) Events() <-chan abi.Event { return t.events }

func (t *timerManager) Close() {
	t.cancel()
	t.mu.Lock()
	for id, timer := range t.active {
		delete(t.active, id)
		close(timer.stop)
	}
	t.pending = nil
	clear(t.queued)
	t.mu.Unlock()
}

func registerTimers(b wazero.HostModuleBuilder, timers timerService) wazero.HostModuleBuilder {
	return b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, delayNanos int64, recurring int32) int32 {
			if timers == nil || (recurring != 0 && recurring != 1) {
				return abi.TimerErrInternal
			}
			return timers.Start(time.Duration(delayNanos), recurring == 1)
		}).
		Export("timer_start").
		NewFunctionBuilder().
		WithFunc(func(_ context.Context, id int32) int32 {
			if timers == nil || id <= 0 {
				return 0
			}
			if timers.Cancel(uint32(id)) {
				return 1
			}
			return 0
		}).
		Export("timer_cancel")
}

// mergedSource serializes a Source with host-generated completion events. Only
// one goroutine ever calls base.Next, while Next selects completions without
// polling. Cancellation ends the pump for context-aware sources.
type mergedSource struct {
	ctx    context.Context
	base   <-chan sourceResult
	extras <-chan abi.Event
}

type sourceResult struct {
	event abi.Event
	ok    bool
}

func newMergedSource(ctx context.Context, base Source, extras <-chan abi.Event) Source {
	results := make(chan sourceResult)
	go func() {
		defer close(results)
		for {
			event, ok := base.Next(ctx)
			select {
			case results <- sourceResult{event: event, ok: ok}:
			case <-ctx.Done():
				return
			}
			if !ok {
				return
			}
		}
	}()
	return &mergedSource{ctx: ctx, base: results, extras: extras}
}

func (s *mergedSource) Next(context.Context) (abi.Event, bool) {
	select {
	case <-s.ctx.Done():
		return abi.Event{}, false
	case event, ok := <-s.extras:
		return event, ok
	case result, ok := <-s.base:
		if !ok {
			return abi.Event{}, false
		}
		return result.event, result.ok
	}
}
