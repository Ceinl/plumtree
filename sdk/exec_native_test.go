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
