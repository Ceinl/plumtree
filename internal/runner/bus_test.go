package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestMemBusDeliversToSubscribers(t *testing.T) {
	bus := NewMemBus()
	a := bus.Open()
	b := bus.Open()
	defer a.Close()
	defer b.Close()

	a.Subscribe("room")
	b.Subscribe("room")

	if n := bus.Publish("room", []byte("hi")); n != 2 {
		t.Fatalf("Publish reached %d subscribers, want 2", n)
	}

	for _, sub := range []Subscriber{a, b} {
		select {
		case ev := <-sub.Events():
			if ev.Kind != abi.KindMessage || ev.Topic != "room" || string(ev.Data) != "hi" {
				t.Fatalf("unexpected event %+v", ev)
			}
		default:
			t.Fatal("subscriber received no message")
		}
	}
}

func TestMemBusTopicIsolation(t *testing.T) {
	bus := NewMemBus()
	s := bus.Open()
	defer s.Close()
	s.Subscribe("room")

	if n := bus.Publish("other", []byte("x")); n != 0 {
		t.Fatalf("publish to unrelated topic reached %d subscribers, want 0", n)
	}
	select {
	case ev := <-s.Events():
		t.Fatalf("received message on wrong topic: %+v", ev)
	default:
	}
}

func TestMemBusCloseUnsubscribes(t *testing.T) {
	bus := NewMemBus()
	s := bus.Open()
	s.Subscribe("room")
	s.Close()

	if n := bus.Publish("room", []byte("x")); n != 0 {
		t.Fatalf("closed subscription still received: %d", n)
	}
}

func TestMemBusBestEffortDrop(t *testing.T) {
	bus := NewMemBus()
	s := bus.Open()
	defer s.Close()
	s.Subscribe("room")

	delivered := 0
	for i := 0; i < busInboxSize+10; i++ {
		delivered += bus.Publish("room", []byte("x"))
	}
	// The inbox holds busInboxSize; further publishes are dropped, never block.
	if delivered != busInboxSize {
		t.Fatalf("delivered %d, want %d (bounded inbox)", delivered, busInboxSize)
	}
}

// A subscription may hold at most busMaxTopicsPerSession distinct topics;
// further subscribes return ErrTooManyBusTopics while duplicates and delivery
// keep working.
func TestSubscriptionTopicCap(t *testing.T) {
	bus := NewMemBus()
	s := bus.Open()
	defer s.Close()

	if err := s.Subscribe("kept"); err != nil {
		t.Fatalf("subscribe within cap: %v", err)
	}
	for i := 1; i < busMaxTopicsPerSession; i++ {
		if err := s.Subscribe(fmt.Sprintf("topic-%d", i)); err != nil {
			t.Fatalf("subscribe %d within cap: %v", i, err)
		}
	}
	if err := s.Subscribe("one-topic-too-many"); !errors.Is(err, ErrTooManyBusTopics) {
		t.Fatalf("over-cap subscribe err = %v, want ErrTooManyBusTopics", err)
	}
	// Duplicates stay harmless even at the cap.
	if err := s.Subscribe("kept"); err != nil {
		t.Fatalf("duplicate subscribe at cap = %v, want nil", err)
	}
	if n := bus.Publish("kept", []byte("x")); n != 1 {
		t.Fatalf("publish after cap rejection reached %d subscribers, want 1", n)
	}
}

// Over the worker protocol a rejected Subscribe must surface as an ordinary
// opResp error payload — not tear the session down — and subscribing within the
// cap must still succeed.
func TestBusSubCapCrossesProcessBoundary(t *testing.T) {
	parentToWorkerR, parentToWorkerW := io.Pipe()
	workerToParentR, workerToParentW := io.Pipe()
	defer func() { _ = parentToWorkerW.Close(); _ = workerToParentR.Close() }()

	bus := NewMemBus()
	sub := bus.Open()
	defer sub.Close()

	parentErr := make(chan error, 1)
	go func() {
		pr := &ProcessRunner{}
		for {
			o, payload, err := readMsg(workerToParentR)
			if err != nil {
				close(parentErr)
				return
			}
			if err := pr.serve(context.Background(), parentToWorkerW, o, payload,
				Capabilities{Bus: bus}, nil, nil, sub, CLIStreams{}); err != nil {
				parentErr <- err
				return
			}
		}
	}()

	proxy := &proxySubscriber{rpc: &workerRPC{in: parentToWorkerR, out: workerToParentW}}
	if err := proxy.Subscribe("first"); err != nil {
		t.Fatalf("in-cap cross-process subscribe: %v", err)
	}
	for i := 1; i < busMaxTopicsPerSession; i++ {
		if err := proxy.Subscribe(fmt.Sprintf("topic-%d", i)); err != nil {
			t.Fatalf("cross-process subscribe %d within cap: %v", i, err)
		}
	}
	if err := proxy.Subscribe("over"); !errors.Is(err, ErrTooManyBusTopics) {
		t.Fatalf("cross-process over-cap subscribe err = %v, want ErrTooManyBusTopics", err)
	}
	// The reply path survived the rejected subscribe: a later call still works.
	if err := proxy.Subscribe("still-alive"); !errors.Is(err, ErrTooManyBusTopics) {
		t.Fatalf("post-rejection subscribe err = %v, want ErrTooManyBusTopics (session alive)", err)
	}
	select {
	case err := <-parentErr:
		t.Fatalf("parent serve loop failed: %v", err)
	default:
	}

	if n := bus.Publish("first", []byte("x")); n != 1 {
		t.Fatalf("publish reached %d subscribers, want 1", n)
	}
	_ = workerToParentW.Close() // end the parent's serve loop
	if err := <-parentErr; err != nil {
		t.Fatalf("parent serve loop failed: %v", err)
	}
}
