package execprotocol

import (
	"reflect"
	"testing"
)

func TestParseExecCommand(t *testing.T) {
	got, err := ParseExecCommand(`get_identity "hello world"`)
	want := []string{"get_identity", "hello world"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("quoted command = %q, %v", got, err)
	}
	got, err = ParseExecCommand("Alice two")
	if err != nil || !reflect.DeepEqual(got, []string{"Alice", "two"}) {
		t.Fatalf("CLI = %q, %v", got, err)
	}
	for _, bad := range []string{"unterminated'", "echo | cat", "echo " + string(make([]byte, 64*1024))} {
		if _, err := ParseExecCommand(bad); err == nil {
			t.Errorf("accepted %q", bad[:min(len(bad), 40)])
		}
	}
}
