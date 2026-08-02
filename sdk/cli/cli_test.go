package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

type countResult struct {
	Count int `json:"count"`
}

func countCommands() Command {
	by := IntFlag("by", "amount to add").WithShort('b').WithDefault(1)
	return Root("count commands",
		New("add", "add to the count").WithFlag(by).WithHandler(func(context Context, _ []string) (Output, error) {
			value, err := context.Int("by")
			if err != nil {
				return Empty(), err
			}
			result := countResult{Count: value}
			return Present(result, func(writer Writer, result countResult) { writer.Printf("Count: %d\n", result.Count) }), nil
		}),
	)
}

func executeForTest(t *testing.T, command Command, args ...string) (Execution, bytes.Buffer, bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	execution := Execute(context.Background(), command, args, Streams{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	return execution, stdout, stderr
}

func TestTypedFlagsAndHumanJSONIdentity(t *testing.T) {
	execution, stdout, stderr := executeForTest(t, countCommands(), "add", "--by", "2")
	if execution.ExitCode != 0 || stdout.String() != "Count: 2\n" || stderr.Len() != 0 {
		t.Fatalf("human execution = %#v stdout=%q stderr=%q", execution, stdout.String(), stderr.String())
	}
	execution, stdout, stderr = executeForTest(t, countCommands(), "add", "-b2", "--json")
	if execution.ExitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("json execution = %#v stderr=%q", execution, stderr.String())
	}
	var body resultBody
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil || !body.OK {
		t.Fatalf("json output = %q err=%v", stdout.String(), err)
	}
	if got := int(body.Result.(map[string]any)["count"].(float64)); got != 2 {
		t.Fatalf("json count = %d", got)
	}
}

func TestHelpUsageErrorsAndTermination(t *testing.T) {
	_, stdout, stderr := executeForTest(t, countCommands(), "--help")
	if !strings.Contains(stdout.String(), "Usage:") || stderr.Len() != 0 {
		t.Fatalf("help stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	execution, _, stderr := executeForTest(t, countCommands(), "add", "--bogus")
	if execution.ExitCode != 2 || !strings.Contains(stderr.String(), "unknown flag") || !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("usage execution = %#v stderr=%q", execution, stderr.String())
	}
	root := Root("raw", New("echo", "echo raw").WithArgs(AnyArgs()).WithHandler(func(_ Context, args []string) (Output, error) {
		return Value(args), nil
	}))
	execution, stdout, _ = executeForTest(t, root, "echo", "--", "--literal")
	if execution.ExitCode != 0 || !strings.Contains(stdout.String(), "--literal") {
		t.Fatalf("termination execution = %#v stdout=%q", execution, stdout.String())
	}
	if err := New("broken", "").Validate(); err == nil {
		t.Fatal("leaf without a handler should be rejected")
	}
	schema, err := countCommands().Schema()
	if err != nil || len(schema.Subcommands) != 1 || schema.Subcommands[0].Name != "add" {
		t.Fatalf("schema = %#v err=%v", schema, err)
	}
}

func ExecuteWithInput(command Command, args []string, input string) (Execution, string, string) {
	var stdout, stderr bytes.Buffer
	execution := Execute(context.Background(), command, args, Streams{Stdin: strings.NewReader(input), Stdout: &stdout, Stderr: &stderr})
	return execution, stdout.String(), stderr.String()
}

func TestTypedArgumentsRepeatedFlagsAndBoundedStdin(t *testing.T) {
	name := StringArg("name", "name")
	tags := StringsFlag("tag", "tag").WithShort('t')
	command := Root("typed", New("read", "read").WithArgs(ExactArgs(1)).WithArgument(name).WithFlag(tags).WithHandler(func(ctx Context, _ []string) (Output, error) {
		value, err := ctx.ArgString("name")
		if err != nil {
			return Empty(), err
		}
		labels, err := ctx.Strings("tag")
		if err != nil {
			return Empty(), err
		}
		return Value(map[string]any{"name": value, "tags": labels}), nil
	}))
	execution, stdout, _ := executeForTest(t, command, "read", "Ada", "-t", "one", "--tag=two", "--json")
	if execution.ExitCode != 0 || !strings.Contains(stdout.String(), "\"one\"") || !strings.Contains(stdout.String(), "\"two\"") {
		t.Fatalf("typed execution = %#v stdout=%q", execution, stdout.String())
	}
	inputCommand := Root("input", New("read", "read").WithHandler(func(ctx Context, _ []string) (Output, error) {
		data, err := io.ReadAll(ctx.Stdin)
		if err != nil {
			return Empty(), err
		}
		return Value(string(data)), nil
	}))
	large := strings.Repeat("x", MaxInput+1)
	execution, _, stderr := ExecuteWithInput(inputCommand, []string{"read"}, large)
	if execution.ExitCode == 0 || !strings.Contains(stderr, "invocation limit exceeded") {
		t.Fatalf("bounded stdin execution = %#v stderr=%q", execution, stderr)
	}
}

func TestStableErrorAndSeparateStderr(t *testing.T) {
	command := Root("errors", New("fail", "fail").WithHandler(func(context Context, _ []string) (Output, error) {
		context.Stderr.Printf("progress\n")
		return Empty(), Error{Code: "not_ready", Message: "try again", ExitCode: 7}
	}))
	execution, stdout, stderr := executeForTest(t, command, "fail", "--json")
	if execution.ExitCode != 7 || stdout.String() == "" || !strings.Contains(stderr.String(), "progress") {
		t.Fatalf("failure execution = %#v stdout=%q stderr=%q", execution, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "try again") == false {
		t.Fatalf("missing stable JSON error: %q", stdout.String())
	}
}

func TestLexer(t *testing.T) {
	got, err := Lex(`add --label "Ada Lovelace" 'two words' escaped\ value`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"add", "--label", "Ada Lovelace", "two words", "escaped value"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("lex = %#v, want %#v", got, want)
	}
	for _, input := range []string{"echo | cat", "echo 'unterminated", "echo \x00"} {
		if _, err := Lex(input); !errors.Is(err, ErrLexer) {
			t.Fatalf("Lex(%q) err = %v", input, err)
		}
	}
}

func TestTreeIsSafeForConcurrentInvocations(t *testing.T) {
	command := countCommands()
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			execution, stdout, _ := executeForTest(t, command, "add", "--by=3")
			if execution.ExitCode != 0 || stdout.String() != "Count: 3\n" {
				t.Errorf("execution = %#v stdout=%q", execution, stdout.String())
			}
		}()
	}
	group.Wait()
}
