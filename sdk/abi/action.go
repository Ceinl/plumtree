package abi

const (
	// ActionArgPrefix is the reserved first guest argument used by the SSH
	// gateway. It is not a shell command and must never be interpreted as one.
	ActionArgPrefix  = "__plumtree_action_v1"
	ActionMaxCommand = 64 * 1024
	ActionMaxName    = 64
	ActionMaxJSON    = 64 * 1024
)
