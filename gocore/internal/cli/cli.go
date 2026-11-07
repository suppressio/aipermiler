// Package cli dispatches the binary's subcommands.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
	"github.com/suppressio/aipermiler/gocore/internal/claude"
	"github.com/suppressio/aipermiler/gocore/internal/logging"
	"github.com/suppressio/aipermiler/gocore/internal/provider"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const (
	// ExitOK indicates successful command execution, including stale values.
	ExitOK = 0
	// ExitProviderError indicates a provider failure.
	ExitProviderError = 1
	// ExitUsage indicates invalid command-line arguments.
	ExitUsage      = 2
	maxSecretBytes = 64 * 1024
)

// Env supplies providers, streams and clock without process-global test state.
type Env struct {
	Providers      provider.Registry
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Now            func() time.Time
	Location       *time.Location
}

// Run executes a subcommand, keeping command output on stdout and logs on stderr.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return RunWith(Env{provider.Default(), stdin, stdout, stderr, time.Now, time.Local}, args)
}

// RunWith executes a command with isolated provider instances and an injected clock.
func RunWith(env Env, args []string) int {
	logger := logging.New(env.Stderr)
	logger.Debug("Starting CLI", "version", buildinfo.Version)
	jsonMode := wantsJSON(args)
	if len(args) == 0 {
		return usageError(env, jsonMode, "A command is required")
	}
	command := args[0]
	switch command {
	case "usage":
		return runUsage(env, args[1:], jsonMode)
	case "token":
		return runToken(env, args[1:], jsonMode)
	case "login":
		return runLogin(env, args[1:], jsonMode)
	case "raw":
		return runRaw(env, args[1:], jsonMode)
	case claude.SinkCommand:
		// Hidden: Claude runs it as the probe's status line, never a user.
		return claude.Sink(args[1:], env.Stdin)
	}
	switch command {
	case "help", "-h", "--help", "version", "paths":
	default:
		return usageError(env, jsonMode, "Unknown command: "+command)
	}
	flags := newFlags(command)
	jsonFlag := flags.Bool("json", false, "Print the versioned JSON document")
	if err := flags.Parse(args[1:]); err != nil {
		return flagError(env, jsonMode, err)
	}
	// Parsing stops at the first positional argument, so the flags after it are
	// unparsed: the pre-parse guess answers that usage error.
	if flags.NArg() != 0 {
		return usageError(env, jsonMode, "Unexpected arguments")
	}
	jsonMode = *jsonFlag
	if command == "paths" {
		return runPaths(env, jsonMode)
	}
	if command != "version" {
		if jsonMode {
			return usageError(env, true, "Help is only available as text")
		}
		printUsage(env.Stdout)
		return ExitOK
	}
	if jsonMode {
		err := json.NewEncoder(env.Stdout).Encode(struct {
			SchemaVersion int    `json:"schemaVersion"`
			Name          string `json:"name"`
			Version       string `json:"version"`
			OS            string `json:"os"`
			Arch          string `json:"arch"`
		}{usage.SchemaVersion, buildinfo.Name, buildinfo.Version, runtime.GOOS, runtime.GOARCH})
		return outputResult(env, err, ExitOK)
	}
	_, err := fmt.Fprintf(env.Stdout, "%s %s (%s/%s)\n", buildinfo.Name, buildinfo.Version, runtime.GOOS, runtime.GOARCH)
	return outputResult(env, err, ExitOK)
}

// runPaths prints the folders a client needs to know about (#180): today the folder
// the Claude probes run in, so a client reading Claude Code sessions skips them.
func runPaths(env Env, jsonMode bool) int {
	probe, err := claude.ProbeDir()
	if err != nil {
		if jsonMode {
			return outputResult(env, usage.WriteError(env.Stdout, "", usage.ClassifyError(err)), ExitProviderError)
		}
		_, _ = fmt.Fprintln(env.Stderr, oneLine(err.Error()))
		return ExitProviderError
	}
	if jsonMode {
		err := json.NewEncoder(env.Stdout).Encode(struct {
			SchemaVersion int    `json:"schemaVersion"`
			ClaudeProbe   string `json:"claudeProbe"`
		}{usage.SchemaVersion, probe})
		return outputResult(env, err, ExitOK)
	}
	_, err = fmt.Fprintf(env.Stdout, "claude probe: %s\n", probe)
	return outputResult(env, err, ExitOK)
}

func runUsage(env Env, args []string, jsonMode bool) int {
	var secretStdin *bool
	cmd, code, ok := parseProviderCommand(env, "usage", args, jsonMode, 25*time.Second, 2*time.Minute, func(_ provider.Provider, fs *flag.FlagSet) {
		secretStdin = fs.Bool("secret-stdin", false, "Read the secret from stdin (at most 64 KiB)")
	})
	if !ok {
		return code
	}
	p := cmd.provider
	secret := ""
	if *secretStdin {
		if !p.AcceptsSecret() {
			return usageError(env, cmd.json, "Provider does not accept a secret on stdin")
		}
		var message string
		if secret, message = readSecret(env.Stdin); message != "" {
			return usageError(env, cmd.json, message)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmd.timeout)
	defer cancel()
	u, err := p.Fetch(ctx, secret)
	// A provider that wraps or replaces the deadline error still reports a timeout;
	// a result that arrived in time is kept even if the deadline passed just after.
	if err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		return providerFailure(env, cmd, err, secret)
	}
	if u.Stale != nil {
		stale := *u.Stale
		stale.Message = redact(stale.Message, secret)
		u.Stale = &stale
	}
	if cmd.json {
		return outputResult(env, usage.WriteSuccess(env.Stdout, p.ID(), u, env.Now()), ExitOK)
	}
	var text strings.Builder
	usage.RenderText(&text, p.DisplayName(), u, env.Now(), env.Location)
	_, err = io.WriteString(env.Stdout, text.String())
	return outputResult(env, err, ExitOK)
}

func newFlags(command string) *flag.FlagSet {
	flags := flag.NewFlagSet(buildinfo.Name+" "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	return flags
}

func flagError(env Env, jsonMode bool, err error) int {
	if errors.Is(err, flag.ErrHelp) && !jsonMode {
		printUsage(env.Stdout)
		return ExitOK
	}
	return usageError(env, jsonMode, err.Error())
}

func usageError(env Env, jsonMode bool, message string) int {
	if jsonMode {
		return outputResult(env, usage.WriteError(env.Stdout, "", &usage.Error{Code: usage.CodeUsage, Message: message}), ExitUsage)
	}
	_, _ = fmt.Fprintln(env.Stderr, oneLine(message))
	printUsage(env.Stderr)
	return ExitUsage
}

func outputResult(env Env, err error, code int) int {
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "Cannot write output: %s\n", oneLine(err.Error()))
		return ExitProviderError
	}
	return code
}

// wantsJSON guesses the output format before the flags are parsed, so that an
// invocation that fails before or during parsing still answers in the format
// asked for. Like the flag package, the last occurrence wins and an explicit
// false value (--json=false) means text. Once parsing succeeds the parsed value
// replaces this guess.
func wantsJSON(args []string) bool {
	want := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if name != "json" || !strings.HasPrefix(arg, "-") {
			continue
		}
		if !hasValue {
			want = true
			continue
		}
		if parsed, err := strconv.ParseBool(value); err == nil {
			want = parsed
		}
	}
	return want
}

func redact(message, secret string) string {
	if secret != "" {
		return strings.ReplaceAll(message, secret, "[redacted]")
	}
	return message
}

func oneLine(message string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(message)
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintf(w, `Usage: %s <command>

Commands:
  usage <provider> [flags]         Fetch account usage
  token set <provider> [flags]     Check a token read from stdin and keep it in the OS keyring
  token delete <provider> [flags]  Remove the account's token from the OS keyring
  login <provider> [flags]         Sign in with an OAuth App (client secret on stdin)
  raw <provider> [flags]           Print the provider's raw usage response (JSON, for diagnostics)
  version [--json]                 Print version and platform
  paths [--json]                   Print the folders a client must know (the Claude probe folder)
  help                             Show this help

Common flags:
  --json                Print the versioned JSON document(s)
  --timeout <duration>  Deadline (usage/token/raw: default 25s, at most 2m; login: default 5m, at most 15m)
  --secret-stdin        usage only: read a secret from stdin (at most 64 KiB, if supported)
`, buildinfo.Name)
}
