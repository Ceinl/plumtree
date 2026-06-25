//go:build !wasip1

package sdk

import (
	"sync"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// Native bus is a process-local pub/sub so `go run .` and tests behave like the
// hosted bus. A native process is a single session, so "other sessions" do not
// exist here; a publish is still delivered back to this process (matching the
// hosted rule that the publisher receives its own message), letting an author
// exercise subscribe/publish locally. Delivery is drained into Model.Update by
// the native runtime loop (see runtui_native.go).
var (
	busMu     sync.Mutex
	busTopics = map[string]bool{}
	busInbox  = make(chan MessageMsg, 256)
)

func busSubscribe(topic string) error {
	if len(topic) == 0 || len(topic) > abi.BusMaxTopic {
		return ErrBusTooLarge
	}
	busMu.Lock()
	busTopics[topic] = true
	busMu.Unlock()
	return nil
}

func busPublish(topic string, data []byte) error {
	if len(topic) == 0 || len(topic) > abi.BusMaxTopic || len(data) > abi.BusMaxData {
		return ErrBusTooLarge
	}
	busMu.Lock()
	subscribed := busTopics[topic]
	busMu.Unlock()
	if !subscribed {
		return nil
	}
	msg := MessageMsg{Topic: topic, Data: append([]byte(nil), data...)}
	select {
	case busInbox <- msg:
	default:
		// Inbox full: drop, matching the hosted best-effort contract.
	}
	return nil
}

// drainBus delivers any queued messages to m. It returns true if at least one
// message was delivered, so the caller can repaint only when something changed.
func drainBus(m Model) bool {
	delivered := false
	for {
		select {
		case msg := <-busInbox:
			m.Update(msg)
			delivered = true
		default:
			return delivered
		}
	}
}
