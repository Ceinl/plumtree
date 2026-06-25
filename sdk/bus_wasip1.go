//go:build wasip1

package sdk

import (
	"runtime"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// Bus host imports. Like recv/present and the kv_* calls, they pass pointers
// into guest linear memory; the host reads the topic and payload bytes.
//
// Delivery of published messages back to this guest does not use a host import:
// the host queues a KindMessage event that the guest receives through the normal
// recv loop (see runtui_wasip1.go), so subscription needs only this register
// call plus the existing event pump.

//go:wasmimport plumtree bus_sub
func hostBusSub(topicPtr, topicLen int32) int32

//go:wasmimport plumtree bus_pub
func hostBusPub(topicPtr, topicLen, dataPtr, dataLen int32) int32

func busSubscribe(topic string) error {
	if len(topic) == 0 || len(topic) > abi.BusMaxTopic {
		return ErrBusTooLarge
	}
	t := []byte(topic)
	r := hostBusSub(bytePtr(t), int32(len(t)))
	runtime.KeepAlive(t)
	return busErr(r)
}

func busPublish(topic string, data []byte) error {
	if len(topic) == 0 || len(topic) > abi.BusMaxTopic || len(data) > abi.BusMaxData {
		return ErrBusTooLarge
	}
	t := []byte(topic)
	r := hostBusPub(bytePtr(t), int32(len(t)), bytePtr(data), int32(len(data)))
	runtime.KeepAlive(t)
	runtime.KeepAlive(data)
	return busErr(r)
}

// busErr maps a host result code to an SDK error. A non-negative code from
// bus_pub is the subscriber count (success).
func busErr(code int32) error {
	switch {
	case code >= 0:
		return nil
	case code == abi.BusErrTooLarge:
		return ErrBusTooLarge
	default:
		return ErrBusUnavailable
	}
}
