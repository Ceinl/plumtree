package control

import (
	"errors"
	"fmt"
	"sync"
)

type SuspensionScope string

const (
	SuspensionOwner  SuspensionScope = "owner"
	SuspensionApp    SuspensionScope = "app"
	SuspensionDeploy SuspensionScope = "deploy"
)

type SuspensionEvent struct {
	Scope SuspensionScope `json:"scope"`
	ID    string          `json:"id"`
}

// SuspensionListener acknowledges an event by returning nil only after all
// matching sessions under its responsibility have stopped.
type SuspensionListener func(SuspensionEvent) error

// RegisterSuspensionListener adds one suspension destination. The returned
// function unregisters it and is safe to call more than once.
func (s *Store) RegisterSuspensionListener(listener SuspensionListener) func() {
	if listener == nil {
		return func() {}
	}
	s.suspensionMu.Lock()
	if s.suspensionListeners == nil {
		s.suspensionListeners = make(map[uint64]SuspensionListener)
	}
	s.nextSuspensionID++
	id := s.nextSuspensionID
	s.suspensionListeners[id] = listener
	s.suspensionMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.suspensionMu.Lock()
			delete(s.suspensionListeners, id)
			s.suspensionMu.Unlock()
		})
	}
}

func (s *Store) publishSuspension(event SuspensionEvent) error {
	s.suspensionMu.Lock()
	listeners := make([]SuspensionListener, 0, len(s.suspensionListeners))
	for _, listener := range s.suspensionListeners {
		listeners = append(listeners, listener)
	}
	s.suspensionMu.Unlock()

	errs := make(chan error, len(listeners))
	var wg sync.WaitGroup
	for _, listener := range listeners {
		wg.Add(1)
		go func(listener SuspensionListener) {
			defer wg.Done()
			if err := listener(event); err != nil {
				errs <- err
			}
		}(listener)
	}
	wg.Wait()
	close(errs)
	var joined []error
	for err := range errs {
		joined = append(joined, err)
	}
	if err := errors.Join(joined...); err != nil {
		return fmt.Errorf("suspension acknowledgement failed: %w", err)
	}
	return nil
}
