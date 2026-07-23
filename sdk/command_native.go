//go:build !wasip1

package sdk

import (
	"sync"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
)

type nativeTimer struct {
	stop chan struct{}
}

type nativeCommandState struct {
	mu      sync.Mutex
	next    CommandID
	active  map[CommandID]*nativeTimer
	pending []CommandID
	queued  map[CommandID]bool
	wake    chan struct{}
}

var nativeCommands = newNativeCommandState()

func newNativeCommandState() *nativeCommandState {
	return &nativeCommandState{
		active: make(map[CommandID]*nativeTimer),
		queued: make(map[CommandID]bool),
		wake:   make(chan struct{}, 1),
	}
}

func scheduleCommand(cmd Command) (CommandID, error) {
	delay := int64(cmd.delay)
	minimum := abi.TimerMinDelayNanos
	if cmd.recurring {
		minimum = abi.TimerMinEveryNanos
	}
	if delay < minimum || delay > abi.TimerMaxDelayNanos {
		return 0, ErrInvalidCommand
	}

	nativeCommands.mu.Lock()
	if len(nativeCommands.active) >= abi.TimerMaxActive {
		nativeCommands.mu.Unlock()
		return 0, ErrCommandLimit
	}
	for {
		nativeCommands.next++
		if nativeCommands.next != 0 {
			if _, exists := nativeCommands.active[nativeCommands.next]; !exists {
				break
			}
		}
	}
	id := nativeCommands.next
	timer := &nativeTimer{stop: make(chan struct{})}
	nativeCommands.active[id] = timer
	nativeCommands.mu.Unlock()

	go nativeCommands.run(id, timer, cmd.delay, cmd.recurring)
	return id, nil
}

func (s *nativeCommandState) run(id CommandID, timer *nativeTimer, delay time.Duration, recurring bool) {
	if recurring {
		ticker := time.NewTicker(delay)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.enqueue(id, true)
			case <-timer.stop:
				return
			}
		}
	}

	clock := time.NewTimer(delay)
	defer clock.Stop()
	select {
	case <-clock.C:
		s.enqueue(id, false)
	case <-timer.stop:
	}
}

func (s *nativeCommandState) enqueue(id CommandID, recurring bool) {
	s.mu.Lock()
	if _, active := s.active[id]; !active {
		s.mu.Unlock()
		return
	}
	if !recurring {
		delete(s.active, id)
	}
	if !s.queued[id] {
		s.queued[id] = true
		s.pending = append(s.pending, id)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func cancelCommand(id CommandID) bool {
	nativeCommands.mu.Lock()
	timer, active := nativeCommands.active[id]
	if active {
		delete(nativeCommands.active, id)
		close(timer.stop)
	}
	delete(nativeCommands.queued, id)
	for i, pending := range nativeCommands.pending {
		if pending == id {
			nativeCommands.pending = append(nativeCommands.pending[:i], nativeCommands.pending[i+1:]...)
			break
		}
	}
	nativeCommands.mu.Unlock()
	return active
}

func drainCommands(m Model) bool {
	nativeCommands.mu.Lock()
	pending := append([]CommandID(nil), nativeCommands.pending...)
	nativeCommands.pending = nativeCommands.pending[:0]
	for _, id := range pending {
		delete(nativeCommands.queued, id)
	}
	nativeCommands.mu.Unlock()
	for _, id := range pending {
		m.Update(TimerMsg{ID: id})
	}
	return len(pending) > 0
}

func stopCommands() {
	nativeCommands.mu.Lock()
	for id, timer := range nativeCommands.active {
		delete(nativeCommands.active, id)
		close(timer.stop)
	}
	nativeCommands.pending = nil
	clear(nativeCommands.queued)
	nativeCommands.mu.Unlock()
}
