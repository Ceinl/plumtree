//go:build !wasip1

package sdk

import "testing"

func TestExecNativeReturnsNonzeroExitAsResult(t *testing.T) {
	result, err := Exec("sh", "-c", "printf hello; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 3 || string(result.Stdout) != "hello" {
		t.Fatalf("result = %#v", result)
	}
}

func TestExecNativeRejectsOversizedOutput(t *testing.T) {
	if _, err := Exec("sh", "-c", "yes x | head -c 1048577"); err != ErrExecTooLarge {
		t.Fatalf("Exec error = %v, want %v", err, ErrExecTooLarge)
	}
}
