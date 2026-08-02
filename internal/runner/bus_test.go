package runner

import (
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
