// Package cli provides the finite, synchronous command lifecycle for Plumtree
// leaves. Command trees are values: execution parses into per-invocation state
// and never mutates the declared tree or process-global argv/stdio.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxArgs        = 64
	MaxArgument    = 256
	MaxCommandName = 32
	MaxSummary     = 256
	MaxDepth       = 8
	MaxCommands    = 128
	MaxFlags       = 32
	MaxOutput      = 64 << 10
	MaxInput       = 1 << 20
	MaxDescription = 1024
)

var (
	ErrUsage       = errors.New("cli: usage error")
	ErrUnknown     = errors.New("cli: unknown command or flag")
	ErrMissing     = errors.New("cli: missing value")
	ErrInvalid     = errors.New("cli: invalid value")
	ErrLimit       = errors.New("cli: invocation limit exceeded")
	ErrUnavailable = errors.New("cli: runtime unavailable")
	ErrInternal    = errors.New("cli: internal error")
	ErrLexer       = errors.New("cli: invalid command string")
	ErrCanceled    = errors.New("cli: invocation canceled")
	namePattern    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	codePattern    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// ValueKind describes how a flag or positional argument is parsed.
type ValueKind uint8

const (
	StringValue ValueKind = iota
	BoolValue
	IntValue
	DurationValue
	StringsValue
)

// ArgsRule bounds positional arguments for one command.
type ArgsRule struct {
	Min int
	Max int
}

func NoArgs() ArgsRule                        { return ArgsRule{} }
func AnyArgs() ArgsRule                       { return ArgsRule{Max: MaxArgs} }
func ExactArgs(n int) ArgsRule                { return ArgsRule{Min: n, Max: n} }
func AtLeastArgs(n int) ArgsRule              { return ArgsRule{Min: n, Max: MaxArgs} }
func AtMostArgs(n int) ArgsRule               { return ArgsRule{Max: n} }
func RangeArgs(minimum, maximum int) ArgsRule { return ArgsRule{Min: minimum, Max: maximum} }

// Flag is an immutable descriptor. Parsed values are kept in an invocation,
// not in the descriptor, so one tree is safe for concurrent sessions.
type Flag struct {
	Name     string
	Short    rune
	Help     string
	Kind     ValueKind
	Default  string
	Required bool
	Repeated bool
	Validate func(string) error
}

func StringFlag(name, help string) Flag { return Flag{Name: name, Help: help, Kind: StringValue} }
func BoolFlag(name, help string) Flag   { return Flag{Name: name, Help: help, Kind: BoolValue} }
func IntFlag(name, help string) Flag    { return Flag{Name: name, Help: help, Kind: IntValue} }
func DurationFlag(name, help string) Flag {
	return Flag{Name: name, Help: help, Kind: DurationValue}
}
func StringsFlag(name, help string) Flag {
	return Flag{Name: name, Help: help, Kind: StringsValue, Repeated: true}
}

func (flag Flag) WithShort(short rune) Flag { flag.Short = short; return flag }
func (flag Flag) WithDefault(value any) Flag {
	flag.Default = formatValue(value)
	return flag
}
func (flag Flag) RequiredValue() Flag { flag.Required = true; return flag }
func (flag Flag) RepeatedValue() Flag { flag.Repeated = true; return flag }
func (flag Flag) WithValidator(validator func(string) error) Flag {
	flag.Validate = validator
	return flag
}

// Argument is an immutable typed positional descriptor. The value is fetched
// from Context with ArgString, ArgBool, ArgInt, ArgDuration, or ArgStrings.
type Argument struct {
	Name string
	Help string
	Kind ValueKind
}

func StringArg(name, help string) Argument {
	return Argument{Name: name, Help: help, Kind: StringValue}
}
func BoolArg(name, help string) Argument { return Argument{Name: name, Help: help, Kind: BoolValue} }
func IntArg(name, help string) Argument  { return Argument{Name: name, Help: help, Kind: IntValue} }
func DurationArg(name, help string) Argument {
	return Argument{Name: name, Help: help, Kind: DurationValue}
}
func StringsArg(name, help string) Argument {
	return Argument{Name: name, Help: help, Kind: StringsValue}
}

// Handler is synchronous finite command work. It receives only invocation
// context and positional tokens; capability operations run directly with the
// embedded context.Context.
type Handler func(Context, []string) (Output, error)

// Command is a bounded immutable-by-convention command tree. Use the With*
// methods to obtain a copy with cloned slices; Execute never mutates it.
type Command struct {
	Name        string
	Summary     string
	Args        ArgsRule
	Arguments   []Argument
	Flags       []Flag
	Subcommands []Command
	Handle      Handler
}

func Root(summary string, children ...Command) Command {
	return Command{Summary: summary, Args: NoArgs(), Subcommands: append([]Command(nil), children...)}
}

func New(name, summary string) Command {
	return Command{Name: name, Summary: summary, Args: NoArgs()}
}

func (command Command) WithArgs(rule ArgsRule) Command {
	command.Args = rule
	return command
}
func (command Command) WithArgument(argument Argument) Command {
	command.Arguments = append(append([]Argument(nil), command.Arguments...), argument)
	return command
}
func (command Command) WithFlag(flag Flag) Command {
	command.Flags = append(append([]Flag(nil), command.Flags...), flag)
	return command
}
func (command Command) WithCommand(child Command) Command {
	command.Subcommands = append(append([]Command(nil), command.Subcommands...), child)
	return command
}
func (command Command) WithHandler(handler Handler) Command { command.Handle = handler; return command }

// Writer is the bounded text sink supplied to stderr and human renderers.
type Writer interface {
	io.Writer
	Printf(string, ...any)
	Print(...any)
	Println(...any)
}

// Streams are the only process I/O boundary. Tests pass buffers; native Run
// supplies os.Stdin, os.Stdout, and os.Stderr.
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Context is read-only invocation state plus bounded stdin/stderr. It embeds
// context.Context so typed capability operations can use it directly.
type Context struct {
	context.Context
	Stdin  io.Reader
	Stderr Writer

	values   map[string]any
	args     map[string]any
	jsonMode bool
	path     string
}

func (context Context) JSON() bool          { return context.jsonMode }
func (context Context) CommandPath() string { return context.path }

func (context Context) String(name string) (string, error) {
	value, err := context.value(name)
	if err != nil {
		return "", err
	}
	parsed, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s is not a string", ErrInvalid, name)
	}
	return parsed, nil
}
func (context Context) Bool(name string) (bool, error) {
	value, err := context.value(name)
	if err != nil {
		return false, err
	}
	parsed, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%w: %s is not a bool", ErrInvalid, name)
	}
	return parsed, nil
}
func (context Context) Int(name string) (int, error) {
	value, err := context.value(name)
	if err != nil {
		return 0, err
	}
	parsed, ok := value.(int)
	if !ok {
		return 0, fmt.Errorf("%w: %s is not an int", ErrInvalid, name)
	}
	return parsed, nil
}
func (context Context) Duration(name string) (time.Duration, error) {
	value, err := context.value(name)
	if err != nil {
		return 0, err
	}
	parsed, ok := value.(time.Duration)
	if !ok {
		return 0, fmt.Errorf("%w: %s is not a duration", ErrInvalid, name)
	}
	return parsed, nil
}
func (context Context) Strings(name string) ([]string, error) {
	value, err := context.value(name)
	if err != nil {
		return nil, err
	}
	parsed, ok := value.([]string)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not repeated strings", ErrInvalid, name)
	}
	return append([]string(nil), parsed...), nil
}
func (context Context) ArgString(name string) (string, error) {
	value, err := context.argument(name)
	if err != nil {
		return "", err
	}
	parsed, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: argument %s", ErrInvalid, name)
	}
	return parsed, nil
}
func (context Context) ArgBool(name string) (bool, error) {
	value, err := context.argument(name)
	if err != nil {
		return false, err
	}
	parsed, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%w: argument %s", ErrInvalid, name)
	}
	return parsed, nil
}
func (context Context) ArgInt(name string) (int, error) {
	value, err := context.argument(name)
	if err != nil {
		return 0, err
	}
	parsed, ok := value.(int)
	if !ok {
		return 0, fmt.Errorf("%w: argument %s", ErrInvalid, name)
	}
	return parsed, nil
}
func (context Context) ArgDuration(name string) (time.Duration, error) {
	value, err := context.argument(name)
	if err != nil {
		return 0, err
	}
	parsed, ok := value.(time.Duration)
	if !ok {
		return 0, fmt.Errorf("%w: argument %s", ErrInvalid, name)
	}
	return parsed, nil
}
func (context Context) ArgStrings(name string) ([]string, error) {
	value, ok := context.args[name]
	if !ok {
		return nil, fmt.Errorf("%w: argument %s", ErrMissing, name)
	}
	parsed, ok := value.([]string)
	if !ok {
		return nil, fmt.Errorf("%w: argument %s", ErrInvalid, name)
	}
	return append([]string(nil), parsed...), nil
}

func (context Context) value(name string) (any, error) {
	value, ok := context.values[name]
	if !ok {
		return nil, fmt.Errorf("%w: flag %s", ErrMissing, name)
	}
	return value, nil
}

func (context Context) argument(name string) (any, error) {
	value, ok := context.args[name]
	if !ok {
		return nil, fmt.Errorf("%w: argument %s", ErrMissing, name)
	}
	return value, nil
}

// Output is a typed result with one human renderer. JSON mode serializes the
// same value, so handlers cannot accidentally implement divergent semantics.
type Output struct {
	value  any
	render func(Writer, any)
	empty  bool
}

func Present[T any](value T, render func(Writer, T)) Output {
	var renderer func(Writer, any)
	if render != nil {
		renderer = func(writer Writer, raw any) { render(writer, raw.(T)) }
	}
	return Output{value: value, render: renderer}
}

func Value(value any) Output { return Output{value: value} }
func Empty() Output          { return Output{empty: true} }

// Error is an expected stable command failure. Exit code 2 is reserved for
// parse/usage failures; application handlers normally use the default 1.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	ExitCode  int    `json:"-"`
	ShowUsage bool   `json:"usage"`
}

func (failure Error) Error() string { return failure.Message }

type errorBody struct {
	OK    bool  `json:"ok"`
	Error Error `json:"error"`
}
type resultBody struct {
	OK     bool `json:"ok"`
	Result any  `json:"result"`
}

// Execution is the deterministic result of one invocation.
type Execution struct {
	ExitCode int
	Err      error
	Path     string
}

// Run executes a native finite leaf using process streams and exits with the
// command's stable status. Tests and embedding code should use Execute.
func Run(command Command) {
	execution := Execute(context.Background(), command, os.Args[1:], Streams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr})
	if execution.ExitCode != 0 {
		os.Exit(execution.ExitCode)
	}
}

// Execute validates, parses, dispatches, and presents one finite invocation.
func Execute(ctx context.Context, root Command, args []string, streams Streams) Execution {
	if ctx == nil {
		ctx = context.Background()
	}
	if streams.Stdin == nil {
		streams.Stdin = strings.NewReader("")
	}
	if streams.Stdout == nil {
		streams.Stdout = io.Discard
	}
	if streams.Stderr == nil {
		streams.Stderr = io.Discard
	}
	if err := root.Validate(); err != nil {
		failure := normalizeFailure(err, true)
		writeFailure(streams, root, failure, false, "")
		return Execution{ExitCode: failure.ExitCode, Err: err}
	}
	invocation, failure := parse(ctx, root, args, streams.Stdin)
	if failure != nil {
		writeFailure(streams, invocation.command, *failure, invocation.jsonMode, invocation.path)
		return Execution{ExitCode: failure.ExitCode, Err: failure, Path: invocation.path}
	}
	if invocation.help {
		if invocation.jsonMode {
			writeJSON(streams.Stdout, invocation.command.schemaValue())
		} else {
			writeBytes(streams.Stdout, []byte(invocation.command.helpText(invocation.path)))
		}
		return Execution{Path: invocation.path}
	}
	if invocation.command.Handle == nil {
		if invocation.jsonMode {
			writeJSON(streams.Stdout, invocation.command.schemaValue())
		} else {
			writeBytes(streams.Stdout, []byte(invocation.command.helpText(invocation.path)))
		}
		return Execution{Path: invocation.path}
	}
	if err := ctx.Err(); err != nil {
		failure := normalizeFailure(err, false)
		writeFailure(streams, invocation.command, failure, invocation.jsonMode, invocation.path)
		return Execution{ExitCode: failure.ExitCode, Err: err, Path: invocation.path}
	}

	stdout := &boundedWriter{limit: MaxOutput}
	stderr := &boundedWriter{limit: MaxOutput}
	callContext, cancel := context.WithCancel(ctx)
	defer cancel()
	call := Context{Context: callContext, Stdin: &boundedReader{reader: streams.Stdin, limit: MaxInput}, Stderr: stderr, values: invocation.values, args: invocation.args, jsonMode: invocation.jsonMode, path: invocation.path}
	var output Output
	var handlerErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				handlerErr = ErrInternal
			}
		}()
		output, handlerErr = invocation.command.Handle(call, invocation.positionals)
	}()
	if stderr.err != nil {
		handlerErr = stderr.err
	}
	if handlerErr != nil {
		failure := normalizeFailure(handlerErr, false)
		writeBytes(streams.Stderr, stderr.bytes())
		writeFailure(streams, invocation.command, failure, invocation.jsonMode, invocation.path)
		return Execution{ExitCode: failure.ExitCode, Err: handlerErr, Path: invocation.path}
	}
	if invocation.jsonMode {
		result := any(nil)
		if !output.empty {
			result = output.value
		}
		encoded, err := json.Marshal(resultBody{OK: true, Result: result})
		if err != nil || len(encoded)+1 > MaxOutput {
			failure := normalizeFailure(ErrInternal, false)
			writeBytes(streams.Stderr, stderr.bytes())
			writeFailure(streams, invocation.command, failure, true, invocation.path)
			return Execution{ExitCode: failure.ExitCode, Err: err, Path: invocation.path}
		}
		encoded = append(encoded, '\n')
		writeBytes(streams.Stdout, encoded)
	} else if !output.empty {
		var renderErr error
		if output.render != nil {
			func() {
				defer func() {
					if recover() != nil {
						renderErr = ErrInternal
					}
				}()
				output.render(stdout, output.value)
			}()
		} else if output.value != nil {
			_, stdout.err = fmt.Fprintln(stdout, output.value)
		}
		if renderErr != nil {
			failure := normalizeFailure(renderErr, false)
			writeBytes(streams.Stderr, stderr.bytes())
			writeFailure(streams, invocation.command, failure, false, invocation.path)
			return Execution{ExitCode: failure.ExitCode, Err: renderErr, Path: invocation.path}
		}
		if stdout.err != nil {
			failure := normalizeFailure(stdout.err, false)
			writeBytes(streams.Stderr, stderr.bytes())
			writeFailure(streams, invocation.command, failure, false, invocation.path)
			return Execution{ExitCode: failure.ExitCode, Err: stdout.err, Path: invocation.path}
		}
		writeBytes(streams.Stdout, stdout.bytes())
	}
	writeBytes(streams.Stderr, stderr.bytes())
	return Execution{Path: invocation.path}
}

func writeFailure(streams Streams, command Command, failure Error, jsonMode bool, path string) {
	if jsonMode {
		encoded, err := json.Marshal(errorBody{Error: failure})
		if err == nil && len(encoded)+1 <= MaxOutput {
			writeBytes(streams.Stdout, append(encoded, '\n'))
		}
		return
	}
	message := failure.Message
	if path != "" {
		message = path + ": " + message
	}
	if failure.ShowUsage {
		message += "\n" + command.helpText(path)
	}
	writeBytes(streams.Stderr, []byte(message+"\n"))
}

func writeJSON(writer io.Writer, value any) {
	encoded, err := json.Marshal(value)
	if err == nil && len(encoded)+1 <= MaxOutput {
		writeBytes(writer, append(encoded, '\n'))
	}
}

func writeBytes(writer io.Writer, bytes []byte) {
	if len(bytes) == 0 {
		return
	}
	_, _ = writer.Write(bytes)
}

func normalizeFailure(err error, usage bool) Error {
	var failure Error
	if errors.As(err, &failure) {
		if failure.Code == "" {
			failure.Code = "internal"
		}
		if failure.Message == "" {
			failure.Message = "command failed"
		}
		if failure.ExitCode == 0 {
			failure.ExitCode = 1
		}
		return sanitizeFailure(failure)
	}
	var pointer *Error
	if errors.As(err, &pointer) && pointer != nil {
		return sanitizeFailure(*pointer)
	}
	code, message, exit := "internal", "internal error", 1
	if errors.Is(err, ErrUsage) || errors.Is(err, ErrUnknown) || errors.Is(err, ErrMissing) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrLexer) {
		code, message, exit = "usage", err.Error(), 2
	}
	if usage {
		code, exit = "invalid_definition", 2
		message = err.Error()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCanceled) {
		code, message, exit = "canceled", "invocation canceled", 1
	}
	if errors.Is(err, ErrLimit) {
		code, message, exit = "limit", "invocation limit exceeded", 1
	}
	return sanitizeFailure(Error{Code: code, Message: message, ExitCode: exit, ShowUsage: usage})
}

func sanitizeFailure(failure Error) Error {
	if !codePattern.MatchString(failure.Code) {
		failure.Code = "internal"
		failure.Message = "internal error"
		failure.ExitCode = 1
	}
	failure.Message = safeText(failure.Message)
	if len(failure.Message) > MaxDescription {
		failure.Message = failure.Message[:MaxDescription]
	}
	return failure
}

type invocation struct {
	command     Command
	values      map[string]any
	args        map[string]any
	positionals []string
	path        string
	jsonMode    bool
	help        bool
}

func parse(ctx context.Context, root Command, raw []string, stdin io.Reader) (invocation, *Error) {
	result := invocation{command: root, values: map[string]any{}, args: map[string]any{}, path: root.Name}
	command := root
	path := []string{}
	if root.Name != "" {
		path = append(path, root.Name)
	}
	positionals := make([]string, 0)
	stopFlags := false
	for index := 0; index < len(raw); index++ {
		if err := ctx.Err(); err != nil {
			return result, &Error{Code: "canceled", Message: ErrCanceled.Error(), ExitCode: 1}
		}
		token := raw[index]
		if len(token) > MaxArgument {
			return result, &Error{Code: "argument_limit", Message: "argument exceeds the maximum length", ExitCode: 2, ShowUsage: true}
		}
		if !stopFlags && token == "--" {
			stopFlags = true
			continue
		}
		if !stopFlags && token == "--help" {
			result.help = true
			continue
		}
		if !stopFlags && token == "--json" {
			result.jsonMode = true
			continue
		}
		if !stopFlags && strings.HasPrefix(token, "--") {
			name, value, hasValue := splitLongFlag(token[2:])
			flag, ok := findFlag(command.Flags, name)
			if !ok {
				return result, unknownFailure("flag", name, flagNames(command.Flags), true)
			}
			consumed, err := consumeFlag(flag, value, hasValue, raw[index+1:])
			if err != nil {
				return result, flagFailure(flag, err)
			}
			if !hasValue && flag.Kind != BoolValue {
				value, hasValue = raw[index+1], true
			}
			if consumed > 0 {
				index += consumed
			}
			if err := setFlag(result.values, flag, value, hasValue, nil); err != nil {
				return result, flagFailure(flag, err)
			}
			continue
		}
		if !stopFlags && strings.HasPrefix(token, "-") && token != "-" {
			consumed, err := parseShortFlags(command.Flags, token[1:], raw[index+1:], result.values)
			if err != nil {
				return result, err
			}
			index += consumed
			continue
		}
		if !stopFlags {
			if child, ok := findCommand(command.Subcommands, token); ok && len(positionals) == 0 {
				command = child
				path = append(path, child.Name)
				positionals = positionals[:0]
				stopFlags = false
				continue
			}
			if len(command.Subcommands) > 0 && command.Args.Max == 0 {
				return result, unknownFailure("command", token, commandNames(command.Subcommands), true)
			}
		}
		positionals = append(positionals, token)
		if len(positionals) > MaxArgs {
			return result, &Error{Code: "argument_limit", Message: "too many arguments", ExitCode: 2, ShowUsage: true}
		}
	}
	if err := validateArgs(command, positionals); err != nil {
		return result, err
	}
	for index, argument := range command.Arguments {
		if index >= len(positionals) {
			continue
		}
		if argument.Kind == StringsValue {
			result.args[argument.Name] = append([]string(nil), positionals[index:]...)
			break
		}
		value, err := parseValue(argument.Kind, positionals[index], false)
		if err != nil {
			return result, &Error{Code: "invalid_argument", Message: argument.Name + ": " + err.Error(), ExitCode: 2, ShowUsage: true}
		}
		result.args[argument.Name] = value
	}
	result.command = command
	result.positionals = append([]string(nil), positionals...)
	result.path = strings.Join(path, " ")
	for _, flag := range command.Flags {
		if _, present := result.values[flag.Name]; present {
			continue
		}
		if flag.Required && flag.Default == "" {
			return result, &Error{Code: "missing_flag", Message: "required flag --" + flag.Name + " is missing", ExitCode: 2, ShowUsage: true}
		}
		if flag.Default != "" || flag.Kind == BoolValue {
			if flag.Repeated {
				if flag.Default == "" {
					result.values[flag.Name] = []string{}
				} else {
					result.values[flag.Name] = strings.Split(flag.Default, ",")
				}
			} else {
				value, err := parseValue(flag.Kind, flag.Default, flag.Kind == BoolValue && flag.Default == "")
				if err != nil {
					return result, flagFailure(flag, err)
				}
				result.values[flag.Name] = value
			}
		}
	}
	_ = stdin
	return result, nil
}

func splitLongFlag(raw string) (name, value string, hasValue bool) {
	if index := strings.IndexByte(raw, '='); index >= 0 {
		return raw[:index], raw[index+1:], true
	}
	return raw, "", false
}

func consumeFlag(flag Flag, value string, hasValue bool, rest []string) (int, error) {
	if flag.Kind == BoolValue {
		if hasValue {
			_, err := parseValue(flag.Kind, value, false)
			return 0, err
		}
		return 0, nil
	}
	if !hasValue {
		if len(rest) == 0 {
			return 0, ErrMissing
		}
		return 1, nil
	}
	return 0, nil
}

func setFlag(values map[string]any, flag Flag, value string, hasValue bool, rest []string) error {
	if !hasValue && flag.Kind == BoolValue {
		value = "true"
		hasValue = true
	} else if !hasValue {
		value = rest[0]
	}
	if flag.Repeated {
		parsed, err := parseValue(StringValue, value, false)
		if err != nil {
			return err
		}
		current, _ := values[flag.Name].([]string)
		values[flag.Name] = append(current, parsed.(string))
		return nil
	}
	parsed, err := parseValue(flag.Kind, value, false)
	if err != nil {
		return err
	}
	if flag.Validate != nil {
		if err := flag.Validate(value); err != nil {
			return err
		}
	}
	values[flag.Name] = parsed
	return nil
}

func parseShortFlags(flags []Flag, raw string, rest []string, values map[string]any) (int, *Error) {
	for offset := 0; offset < len(raw); offset++ {
		flag, ok := findShortFlag(flags, rune(raw[offset]))
		if !ok {
			return 0, unknownFailure("flag", "-"+string(raw[offset]), shortNames(flags), true)
		}
		if flag.Kind == BoolValue {
			values[flag.Name] = true
			continue
		}
		value := raw[offset+1:]
		consumed := 0
		if value == "" {
			if len(rest) == 0 {
				return 0, &Error{Code: "missing_value", Message: "flag -" + string(flag.Short) + " requires a value", ExitCode: 2, ShowUsage: true}
			}
			value, consumed = rest[0], 1
		}
		if err := setFlag(values, flag, value, true, nil); err != nil {
			return consumed, flagFailure(flag, err)
		}
		break
	}
	return consumedForShort(raw, flags, rest), nil
}

func consumedForShort(raw string, flags []Flag, rest []string) int {
	for _, flag := range flags {
		if flag.Short != 0 && strings.ContainsRune(raw, flag.Short) && flag.Kind != BoolValue && len(raw) == 1 && len(rest) > 0 {
			return 1
		}
	}
	return 0
}

func parseValue(kind ValueKind, raw string, emptyBool bool) (any, error) {
	switch kind {
	case StringValue:
		if len(raw) > MaxArgument {
			return nil, ErrLimit
		}
		return raw, nil
	case BoolValue:
		if emptyBool {
			return false, nil
		}
		value, err := strconv.ParseBool(raw)
		return value, err
	case IntValue:
		value, err := strconv.Atoi(raw)
		return value, err
	case DurationValue:
		value, err := time.ParseDuration(raw)
		return value, err
	default:
		return nil, ErrInvalid
	}
}

func validateArgs(command Command, args []string) *Error {
	rule := command.Args
	if rule.Max == 0 && rule.Min == 0 && len(args) == 0 {
		return nil
	}
	if len(args) < rule.Min || (rule.Max >= 0 && len(args) > rule.Max) {
		return &Error{Code: "invalid_arguments", Message: fmt.Sprintf("want %s arguments, got %d", ruleText(rule), len(args)), ExitCode: 2, ShowUsage: true}
	}
	return nil
}

func ruleText(rule ArgsRule) string {
	if rule.Min == rule.Max {
		return strconv.Itoa(rule.Min)
	}
	if rule.Max == MaxArgs {
		return fmt.Sprintf("at least %d", rule.Min)
	}
	return fmt.Sprintf("%d..%d", rule.Min, rule.Max)
}

func flagFailure(flag Flag, err error) *Error {
	code := "invalid_value"
	if errors.Is(err, ErrMissing) {
		code = "missing_value"
	}
	return &Error{Code: code, Message: "--" + flag.Name + ": " + err.Error(), ExitCode: 2, ShowUsage: true}
}

func unknownFailure(kind, name string, candidates []string, usage bool) *Error {
	message := "unknown " + kind + ": " + name
	if suggestion, ok := suggestion(name, candidates); ok {
		message += "; did you mean " + suggestion + "?"
	}
	return &Error{Code: "unknown_" + kind, Message: message, ExitCode: 2, ShowUsage: usage}
}

func suggestion(name string, candidates []string) (string, bool) {
	best, distance := "", 3
	for _, candidate := range candidates {
		current := editDistance(name, strings.TrimLeft(candidate, "-"))
		if current < distance {
			best, distance = candidate, current
		} else if current == distance {
			best = ""
		}
	}
	return best, best != ""
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	for index := range previous {
		previous[index] = index
	}
	for i, left := range a {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		for j, right := range b {
			cost := 0
			if left != right {
				cost = 1
			}
			current[j+1] = min(previous[j+1]+1, current[j]+1, previous[j]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

func findFlag(flags []Flag, name string) (Flag, bool) {
	for _, flag := range flags {
		if flag.Name == name {
			return flag, true
		}
	}
	return Flag{}, false
}
func findShortFlag(flags []Flag, short rune) (Flag, bool) {
	for _, flag := range flags {
		if flag.Short == short {
			return flag, true
		}
	}
	return Flag{}, false
}
func findCommand(commands []Command, name string) (Command, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return Command{}, false
}
func flagNames(flags []Flag) []string {
	result := make([]string, 0, len(flags))
	for _, flag := range flags {
		result = append(result, "--"+flag.Name)
	}
	return result
}
func shortNames(flags []Flag) []string {
	result := make([]string, 0, len(flags))
	for _, flag := range flags {
		if flag.Short != 0 {
			result = append(result, "-"+string(flag.Short))
		}
	}
	return result
}
func commandNames(commands []Command) []string {
	result := make([]string, 0, len(commands))
	for _, command := range commands {
		result = append(result, command.Name)
	}
	return result
}

// Validate checks the complete tree before any handler can run.
func (command Command) Validate() error {
	count := 0
	return validateCommand(command, 0, &count)
}

func validateCommand(command Command, depth int, count *int) error {
	if depth > MaxDepth {
		return fmt.Errorf("%w: command tree exceeds depth %d", ErrLimit, MaxDepth)
	}
	(*count)++
	if *count > MaxCommands {
		return fmt.Errorf("%w: command tree exceeds %d commands", ErrLimit, MaxCommands)
	}
	if command.Name != "" && (!namePattern.MatchString(command.Name) || len(command.Name) > MaxCommandName) {
		return fmt.Errorf("%w: invalid command name %q", ErrUsage, command.Name)
	}
	if strings.IndexFunc(command.Summary, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || len(command.Summary) > MaxSummary {
		return fmt.Errorf("%w: invalid summary for %q", ErrUsage, command.Name)
	}
	if command.Args.Min < 0 || command.Args.Max < 0 || command.Args.Min > command.Args.Max || command.Args.Max > MaxArgs {
		return fmt.Errorf("%w: invalid argument rule for %q", ErrUsage, command.Name)
	}
	if len(command.Flags) > MaxFlags || len(command.Arguments) > MaxArgs {
		return fmt.Errorf("%w: command %q has too many descriptors", ErrLimit, command.Name)
	}
	seenFlags := map[string]bool{"help": true, "json": true}
	seenShort := map[rune]bool{}
	for _, flag := range command.Flags {
		if !namePattern.MatchString(flag.Name) || seenFlags[flag.Name] {
			return fmt.Errorf("%w: duplicate or reserved flag %q", ErrUsage, flag.Name)
		}
		if flag.Kind > StringsValue {
			return fmt.Errorf("%w: invalid value kind for flag %q", ErrUsage, flag.Name)
		}
		if len(flag.Help) > MaxDescription || strings.IndexFunc(flag.Help, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return fmt.Errorf("%w: invalid help for flag %q", ErrUsage, flag.Name)
		}
		if flag.Short != 0 && (flag.Short == '-' || flag.Short == 'h' || flag.Short == 'j' || !utf8.ValidRune(flag.Short) || seenShort[flag.Short]) {
			return fmt.Errorf("%w: invalid short flag for %q", ErrUsage, flag.Name)
		}
		seenFlags[flag.Name] = true
		if flag.Short != 0 {
			seenShort[flag.Short] = true
		}
	}
	seenArgs := map[string]bool{}
	for _, argument := range command.Arguments {
		if !namePattern.MatchString(argument.Name) || seenArgs[argument.Name] {
			return fmt.Errorf("%w: duplicate argument %q", ErrUsage, argument.Name)
		}
		if argument.Kind > StringsValue {
			return fmt.Errorf("%w: invalid value kind for argument %q", ErrUsage, argument.Name)
		}
		seenArgs[argument.Name] = true
	}
	seenCommands := map[string]bool{}
	for _, child := range command.Subcommands {
		if child.Name == "" || seenCommands[child.Name] {
			return fmt.Errorf("%w: duplicate or empty subcommand", ErrUsage)
		}
		seenCommands[child.Name] = true
		if err := validateCommand(child, depth+1, count); err != nil {
			return err
		}
	}
	if command.Name != "" && len(command.Subcommands) == 0 && command.Handle == nil {
		return fmt.Errorf("%w: command %q has no handler", ErrUsage, command.Name)
	}
	return nil
}

// Schema is the bounded machine-readable command description used by agents.
type Schema struct {
	Name        string       `json:"name"`
	Summary     string       `json:"summary,omitempty"`
	Args        ArgsRule     `json:"args"`
	Arguments   []Argument   `json:"arguments,omitempty"`
	Flags       []FlagSchema `json:"flags,omitempty"`
	Subcommands []Schema     `json:"subcommands,omitempty"`
}

type FlagSchema struct {
	Name     string    `json:"name"`
	Short    string    `json:"short,omitempty"`
	Help     string    `json:"help,omitempty"`
	Kind     ValueKind `json:"kind"`
	Default  string    `json:"default,omitempty"`
	Required bool      `json:"required,omitempty"`
	Repeated bool      `json:"repeated,omitempty"`
}

func (command Command) Schema() (Schema, error) {
	if err := command.Validate(); err != nil {
		return Schema{}, err
	}
	return command.schemaValue(), nil
}

func (command Command) schemaValue() Schema {
	schema := Schema{Name: command.Name, Summary: command.Summary, Args: command.Args, Arguments: append([]Argument(nil), command.Arguments...)}
	for _, flag := range command.Flags {
		schema.Flags = append(schema.Flags, FlagSchema{Name: flag.Name, Short: string(flag.Short), Help: flag.Help, Kind: flag.Kind, Default: flag.Default, Required: flag.Required, Repeated: flag.Repeated})
	}
	for _, child := range command.Subcommands {
		schema.Subcommands = append(schema.Subcommands, child.schemaValue())
	}
	return schema
}

func (schema Schema) JSON() ([]byte, error) { return json.Marshal(schema) }

func (command Command) Help() (string, error) {
	if err := command.Validate(); err != nil {
		return "", err
	}
	return command.helpText(command.Name), nil
}

func (command Command) helpText(path string) string {
	var builder strings.Builder
	usage := strings.TrimSpace(path)
	if usage == "" {
		usage = "<command>"
	}
	builder.WriteString("Usage: ")
	builder.WriteString(usage)
	if len(command.Subcommands) > 0 {
		builder.WriteString(" <command>")
	}
	if len(command.Flags) > 0 {
		builder.WriteString(" [flags]")
	}
	if command.Args.Max > 0 {
		builder.WriteString(" [args]")
	}
	builder.WriteString("\n")
	if command.Summary != "" {
		builder.WriteString("\n")
		builder.WriteString(command.Summary)
		builder.WriteString("\n")
	}
	if len(command.Subcommands) > 0 {
		builder.WriteString("\nCommands:\n")
		for _, child := range command.Subcommands {
			builder.WriteString("  ")
			builder.WriteString(child.Name)
			builder.WriteString("\t")
			builder.WriteString(child.Summary)
			builder.WriteString("\n")
		}
	}
	if len(command.Flags) > 0 {
		builder.WriteString("\nFlags:\n  --help\tshow help\n  --json\twrite one JSON result\n")
		for _, flag := range command.Flags {
			builder.WriteString("  --")
			builder.WriteString(flag.Name)
			if flag.Short != 0 {
				builder.WriteString(", -")
				builder.WriteRune(flag.Short)
			}
			builder.WriteString("\t")
			builder.WriteString(flag.Help)
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

// Lex splits one SSH-style command string without invoking a shell. It allows
// unquoted words, single/double quotes, and backslash escapes only.
func Lex(command string) ([]string, error) {
	if len(command) > MaxInput {
		return nil, fmt.Errorf("%w: command is too large", ErrLexer)
	}
	words := make([]string, 0)
	var word strings.Builder
	inWord := false
	quote := byte(0)
	for index := 0; index < len(command); index++ {
		ch := command[index]
		if ch == 0 || ch == 0x7f || (ch < 0x20 && ch != ' ' && ch != '\t') {
			return nil, fmt.Errorf("%w: control byte", ErrLexer)
		}
		if quote == 0 {
			switch ch {
			case '\'', '"':
				quote, inWord = ch, true
			case '\\':
				if index+1 >= len(command) {
					return nil, fmt.Errorf("%w: trailing escape", ErrLexer)
				}
				index++
				word.WriteByte(command[index])
				inWord = true
			case ' ', '\t':
				if inWord {
					if err := appendLexWord(&words, &word); err != nil {
						return nil, err
					}
					inWord = false
				}
			case '|', ';', '&', '>', '<', '`':
				return nil, fmt.Errorf("%w: shell operator", ErrLexer)
			default:
				word.WriteByte(ch)
				inWord = true
			}
			continue
		}
		if ch == quote {
			quote = 0
			continue
		}
		if quote == '"' && ch == '\\' {
			if index+1 >= len(command) {
				return nil, fmt.Errorf("%w: trailing escape", ErrLexer)
			}
			index++
			word.WriteByte(command[index])
			continue
		}
		word.WriteByte(ch)
	}
	if quote != 0 {
		return nil, fmt.Errorf("%w: unterminated quote", ErrLexer)
	}
	if inWord {
		if err := appendLexWord(&words, &word); err != nil {
			return nil, err
		}
	}
	return words, nil
}

func appendLexWord(words *[]string, word *strings.Builder) error {
	if word.Len() > MaxArgument {
		return fmt.Errorf("%w: argument is too large", ErrLexer)
	}
	if len(*words) >= MaxArgs {
		return fmt.Errorf("%w: too many arguments", ErrLexer)
	}
	*words = append(*words, word.String())
	word.Reset()
	return nil
}

type boundedReader struct {
	reader io.Reader
	limit  int
	read   int
}

func (reader *boundedReader) Read(target []byte) (int, error) {
	if reader.read >= reader.limit {
		return 0, ErrLimit
	}
	if remaining := reader.limit - reader.read; len(target) > remaining {
		target = target[:remaining]
	}
	n, err := reader.reader.Read(target)
	reader.read += n
	return n, err
}

type boundedWriter struct {
	buffer bytesBuffer
	limit  int
	err    error
}

func (writer *boundedWriter) Write(data []byte) (int, error) {
	if writer.err != nil {
		return 0, writer.err
	}
	text := safeText(string(data))
	if len(writer.buffer.data)+len(text) > writer.limit {
		writer.err = ErrLimit
		return 0, writer.err
	}
	writer.buffer.data = append(writer.buffer.data, text...)
	return len(data), nil
}
func (writer *boundedWriter) Printf(format string, args ...any) {
	_, _ = writer.Write([]byte(fmt.Sprintf(format, args...)))
}
func (writer *boundedWriter) Print(args ...any)   { _, _ = writer.Write([]byte(fmt.Sprint(args...))) }
func (writer *boundedWriter) Println(args ...any) { _, _ = writer.Write([]byte(fmt.Sprintln(args...))) }
func (writer *boundedWriter) bytes() []byte       { return append([]byte(nil), writer.buffer.data...) }

type bytesBuffer struct{ data []byte }

func safeText(value string) string {
	value = strings.ToValidUTF8(value, "�")
	var builder strings.Builder
	for _, runeValue := range value {
		if runeValue == '\n' || runeValue == '\r' || runeValue == '\t' || (runeValue >= 0x20 && runeValue != 0x7f) {
			builder.WriteRune(runeValue)
		}
	}
	return builder.String()
}

func formatValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case time.Duration:
		return typed.String()
	case []string:
		return strings.Join(typed, ",")
	default:
		return fmt.Sprint(value)
	}
}
