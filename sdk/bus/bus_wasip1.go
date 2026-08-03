//go:build wasip1

package bus

import (
	"context"
	"runtime"
	"unsafe"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/app"
)

//go:wasmimport plumtree bus_sub
func hostBusSub(topicPtr, topicLen int32) int32

//go:wasmimport plumtree bus_pub
func hostBusPub(topicPtr, topicLen, dataPtr, dataLen int32) int32

func bytePtr(value []byte) int32 {
	if len(value) == 0 {
		return 0
	}
	return int32(uintptr(unsafe.Pointer(&value[0])))
}

func registerTopic(topic string) {
	if len(topic) == 0 || len(topic) > abi.BusMaxTopic {
		return
	}
	topicBytes := []byte(topic)
	_ = hostBusSub(bytePtr(topicBytes), int32(len(topicBytes)))
	runtime.KeepAlive(topicBytes)
}

func publish(topic string, data []byte) error {
	if err := validate(topic, data); err != nil {
		return err
	}
	topicBytes := []byte(topic)
	result := hostBusPub(bytePtr(topicBytes), int32(len(topicBytes)), bytePtr(data), int32(len(data)))
	runtime.KeepAlive(topicBytes)
	runtime.KeepAlive(data)
	if result >= 0 {
		return nil
	}
	if result == abi.BusErrTooLarge {
		return ErrTooLarge
	}
	return ErrUnavailable
}

func messageSource(topic string, mapper func(Message) app.Event) func(context.Context, func(app.Event)) {
	return func(ctx context.Context, _ func(app.Event)) {
		if topic == "" || mapper == nil {
			return
		}
		topicBytes := []byte(topic)
		result := hostBusSub(bytePtr(topicBytes), int32(len(topicBytes)))
		runtime.KeepAlive(topicBytes)
		if result < 0 {
			return
		}
		<-ctx.Done()
	}
}
