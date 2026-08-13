package runner

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestCleanAppHostedByBothRunners(t *testing.T) {
	wasm := buildGuest(t, "../../sdk/examples/clean-counter")
	t.Run("in-process", func(t *testing.T) {
		var sink capture
		source := NewScriptSource(40, 8, []string{"enter", "q"})
		if err := Run(context.Background(), wasm, DefaultLimits, Capabilities{}, source, &sink, io.Discard); err != nil {
			t.Fatal(err)
		}
		assertCleanCounterFrames(t, sink)
	})
	t.Run("isolated-worker", func(t *testing.T) {
		var sink capture
		source := NewScriptSource(40, 8, []string{"enter", "q"})
		if err := NewProcessRunner(buildWorker(t)).Run(context.Background(), wasm, DefaultLimits, Capabilities{}, source, &sink, io.Discard); err != nil {
			t.Fatal(err)
		}
		assertCleanCounterFrames(t, sink)
	})
}

func assertCleanCounterFrames(t *testing.T, sink capture) {
	t.Helper()
	if len(sink.frames) < 3 {
		t.Fatalf("got %d frames, want initial, update, quit", len(sink.frames))
	}
	if !frameWith(sink.frames, "Clean counter") || !frameWith(sink.frames, "Count: 1") {
		t.Fatalf("clean app frames did not cross ABI: %s", strings.Join(frameTexts(sink.frames), "\n---\n"))
	}
	if !sink.frames[len(sink.frames)-1].Quit {
		t.Fatal("clean app final frame should carry quit flag")
	}
}

func frameTexts(frames []abi.Frame) []string {
	texts := make([]string, len(frames))
	for i, frame := range frames {
		texts[i] = frameText(frame)
	}
	return texts
}

func TestCleanCLIStreamsAndArgvHostedByBothRunners(t *testing.T) {
	wasm := buildGuest(t, "testdata/cleancli", "GOWORK=off")
	for _, test := range []struct {
		name string
		run  func(*strings.Builder, *strings.Builder) error
	}{
		{name: "in-process", run: func(stdout, stderr *strings.Builder) error {
			return RunCLIWithStreams(context.Background(), wasm, DefaultLimits, Capabilities{}, []string{"greet", "Ada"}, CLIStreams{Stdout: stdout, Stderr: stderr})
		}},
		{name: "isolated-worker", run: func(stdout, stderr *strings.Builder) error {
			return NewProcessRunner(buildWorker(t)).RunCLIWithStreams(context.Background(), wasm, DefaultLimits, Capabilities{}, []string{"greet", "Ada"}, CLIStreams{Stdout: stdout, Stderr: stderr})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if err := test.run(&stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if got := stdout.String(); got != "clean-cli-ok\n" {
				t.Fatalf("stdout = %q", got)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestCleanCLIStdinHostedByBothRunners(t *testing.T) {
	wasm := buildGuest(t, "testdata/cleancli", "GOWORK=off")
	for _, test := range []struct {
		name string
		run  func(CLIStreams) error
	}{
		{name: "in-process", run: func(streams CLIStreams) error {
			return RunCLIWithStreams(context.Background(), wasm, DefaultLimits, Capabilities{}, []string{"stdin"}, streams)
		}},
		{name: "isolated-worker", run: func(streams CLIStreams) error {
			return NewProcessRunner(buildWorker(t)).RunCLIWithStreams(context.Background(), wasm, DefaultLimits, Capabilities{}, []string{"stdin"}, streams)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			streams := CLIStreams{Stdin: strings.NewReader("clean-cli-input"), Stdout: &stdout, Stderr: &stderr}
			if err := test.run(streams); err != nil {
				t.Fatal(err)
			}
			if got := stdout.String(); got != "clean-cli-input\n" {
				t.Fatalf("stdout = %q", got)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestCleanCLIExitAndStderrParity(t *testing.T) {
	wasm := buildGuest(t, "testdata/cleancli", "GOWORK=off")
	for _, test := range []struct {
		name string
		run  func(*strings.Builder, *strings.Builder) error
	}{
		{name: "in-process", run: func(stdout, stderr *strings.Builder) error {
			return RunCLIWithStreams(context.Background(), wasm, DefaultLimits, Capabilities{}, []string{"fail"}, CLIStreams{Stdout: stdout, Stderr: stderr})
		}},
		{name: "isolated-worker", run: func(stdout, stderr *strings.Builder) error {
			return NewProcessRunner(buildWorker(t)).RunCLIWithStreams(context.Background(), wasm, DefaultLimits, Capabilities{}, []string{"fail"}, CLIStreams{Stdout: stdout, Stderr: stderr})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if err := test.run(&stdout, &stderr); err == nil || !strings.Contains(err.Error(), "code 7") {
				t.Fatalf("error = %v, want exit code 7", err)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "clean-cli-stderr") || !strings.Contains(stderr.String(), "clean-cli-failed") {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}
