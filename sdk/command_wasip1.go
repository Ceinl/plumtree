//go:build wasip1

package sdk

import "github.com/Ceinl/plumtree/sdk/abi"

//go:wasmimport plumtree timer_start
func hostTimerStart(delayNanos int64, recurring int32) int32

//go:wasmimport plumtree timer_cancel
func hostTimerCancel(id int32) int32

func scheduleCommand(cmd Command) (CommandID, error) {
	var recurring int32
	if cmd.recurring {
		recurring = 1
	}
	id := hostTimerStart(int64(cmd.delay), recurring)
	switch id {
	case abi.TimerErrInvalid:
		return 0, ErrInvalidCommand
	case abi.TimerErrLimit:
		return 0, ErrCommandLimit
	default:
		if id <= 0 {
			return 0, ErrCommandUnavailable
		}
		return CommandID(id), nil
	}
}

func cancelCommand(id CommandID) bool {
	return hostTimerCancel(int32(id)) == 1
}
