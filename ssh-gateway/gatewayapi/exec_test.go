package gatewayapi

import (
	"reflect"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestParseExecCommand(t *testing.T) {
	got, err := ParseExecCommand(`action create_task {"title":"hello world"}`)
	want := []string{abi.ActionArgPrefix, "create_task", `{"title":"hello world"}`}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("action = %q, %v", got, err)
	}
	got, err = ParseExecCommand("Alice two")
	if err != nil || !reflect.DeepEqual(got, []string{"Alice", "two"}) {
		t.Fatalf("CLI = %q, %v", got, err)
	}
	for _, bad := range []string{"action", "action BAD {}", "action ok {", "actionx nope" + string(make([]byte, abi.ActionMaxCommand))} {
		if _, err := ParseExecCommand(bad); err == nil {
			t.Errorf("accepted %q", bad[:min(len(bad), 40)])
		}
	}
}
