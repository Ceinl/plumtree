package sdk

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestDispatchActionEnvelopes(t *testing.T) {
	actions := Actions{
		"echo": func(_ Ctx, raw json.RawMessage) (any, error) {
			var value map[string]any
			return value, json.Unmarshal(raw, &value)
		},
		"conflict": func(Ctx, json.RawMessage) (any, error) {
			return nil, &ActionError{Code: "conflict", Message: "stale task"}
		},
		"internal": func(Ctx, json.RawMessage) (any, error) { return nil, errors.New("secret detail") },
	}
	if got := string(dispatchAction([]string{abi.ActionArgPrefix, "echo", `{"n":2}`}, actions)); !strings.Contains(got, `"ok":true`) || !strings.Contains(got, `"n":2`) {
		t.Fatalf("success = %s", got)
	}
	if got := string(dispatchAction([]string{abi.ActionArgPrefix, "missing", `{}`}, actions)); !strings.Contains(got, `"code":"unknown_action"`) {
		t.Fatalf("unknown = %s", got)
	}
	if got := string(dispatchAction([]string{abi.ActionArgPrefix, "conflict", `{}`}, actions)); !strings.Contains(got, `"code":"conflict"`) {
		t.Fatalf("typed error = %s", got)
	}
	if got := string(dispatchAction([]string{abi.ActionArgPrefix, "internal", `{}`}, actions)); strings.Contains(got, "secret detail") || !strings.Contains(got, `"code":"internal"`) {
		t.Fatalf("internal = %s", got)
	}
	if got := string(dispatchAction([]string{abi.ActionArgPrefix, "echo", `{`}, actions)); !strings.Contains(got, `"code":"invalid_request"`) {
		t.Fatalf("invalid = %s", got)
	}
}

func TestRunActionIfRequestedHandlesEmptyWASIArgv(t *testing.T) {
	old := os.Args
	os.Args = nil
	t.Cleanup(func() { os.Args = old })
	if runActionIfRequested(nil) {
		t.Fatal("empty argv must enter the normal app mode")
	}
}
