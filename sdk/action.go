package sdk

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"

	"github.com/Ceinl/plumtree/sdk/abi"
)

type ActionHandler func(Ctx, json.RawMessage) (any, error)
type Actions map[string]ActionHandler

// ActionError is returned by handlers for a stable machine-readable failure.
type ActionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ActionError) Error() string { return e.Message }

var actionNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// RunTUIWithActions dispatches a reserved action invocation when present;
// otherwise it enters the normal interactive TUI loop.
func RunTUIWithActions(m Model, meta Meta, actions Actions) {
	if runActionIfRequested(actions) {
		return
	}
	RunTUI(m, meta)
}

// CLIWithActions preserves ordinary CLI arguments and dispatches reserved app
// actions before calling handler.
func CLIWithActions(meta Meta, actions Actions, handler func(Ctx, []string) error) {
	if runActionIfRequested(actions) {
		return
	}
	CLI(meta, handler)
}

func runActionIfRequested(actions Actions) bool {
	if len(os.Args) == 0 {
		return false
	}
	args := os.Args[1:]
	if len(args) == 0 || args[0] != abi.ActionArgPrefix {
		return false
	}
	_, _ = os.Stdout.Write(dispatchAction(args, actions))
	return true
}

type actionEnvelope struct {
	OK     bool         `json:"ok"`
	Result any          `json:"result"`
	Error  *ActionError `json:"error,omitempty"`
}

func dispatchAction(args []string, actions Actions) []byte {
	fail := func(code, message string) []byte {
		out, _ := json.Marshal(actionEnvelope{OK: false, Error: &ActionError{Code: code, Message: message}})
		return append(out, '\n')
	}
	if len(args) != 3 || args[0] != abi.ActionArgPrefix {
		return fail("invalid_request", "reserved action arguments are malformed")
	}
	name, raw := args[1], json.RawMessage(args[2])
	if !actionNameRE.MatchString(name) || len(name) > abi.ActionMaxName || len(raw) > abi.ActionMaxJSON || !json.Valid(raw) {
		return fail("invalid_request", "action name or JSON arguments are invalid")
	}
	handler := actions[name]
	if handler == nil {
		return fail("unknown_action", "unknown action: "+name)
	}
	result, err := handler(Ctx{out: &Out{w: io.Discard}}, raw)
	if err != nil {
		var actionErr *ActionError
		if errors.As(err, &actionErr) && actionErr.Code != "" {
			return fail(actionErr.Code, actionErr.Message)
		}
		return fail("internal", "action failed")
	}
	out, err := json.Marshal(actionEnvelope{OK: true, Result: result})
	if err != nil {
		return fail("internal", "action result is not JSON serializable")
	}
	return append(out, '\n')
}
