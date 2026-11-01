package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/provider"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// providerCommand is a parsed `<command> <provider> [flags]` invocation.
type providerCommand struct {
	provider provider.Provider
	json     bool
	timeout  time.Duration
}

// parseProviderCommand resolves the provider, binds its flags plus the common ones and
// the command's own (bind), parses, and validates. On failure it has already written
// the usage error and returns its exit code with ok false.
func parseProviderCommand(env Env, name string, args []string, jsonMode bool, defaultTimeout, maxTimeout time.Duration, bind func(provider.Provider, *flag.FlagSet)) (providerCommand, int, bool) {
	if len(args) == 0 {
		return providerCommand{}, usageError(env, jsonMode, "A provider is required"), false
	}
	if args[0] == "--help" || args[0] == "-h" {
		return providerCommand{}, flagError(env, jsonMode, flag.ErrHelp), false
	}
	factory, ok := env.Providers[args[0]]
	if !ok {
		return providerCommand{}, usageError(env, jsonMode, "Unknown provider: "+args[0]), false
	}
	p := factory()
	flags := newFlags(name + " " + p.ID())
	jsonFlag := flags.Bool("json", false, "Print the versioned JSON document")
	timeout := flags.Duration("timeout", defaultTimeout, "Deadline")
	p.BindFlags(flags)
	if bind != nil {
		bind(p, flags)
	}
	if err := flags.Parse(args[1:]); err != nil {
		return providerCommand{}, flagError(env, jsonMode, err), false
	}
	// Parsing stops at the first positional argument, so the flags after it are
	// unparsed: the pre-parse guess answers that usage error.
	if flags.NArg() != 0 {
		return providerCommand{}, usageError(env, jsonMode, "Unexpected arguments"), false
	}
	jsonMode = *jsonFlag
	if *timeout <= 0 || *timeout > maxTimeout {
		return providerCommand{}, usageError(env, jsonMode, fmt.Sprintf("Timeout must be greater than 0 and at most %s", maxTimeout)), false
	}
	if v, ok := p.(provider.Validator); ok {
		if err := v.Validate(); err != nil {
			return providerCommand{}, usageError(env, jsonMode, err.Error()), false
		}
	}
	return providerCommand{provider: p, json: jsonMode, timeout: *timeout}, ExitOK, true
}

// readSecret reads one secret from stdin (at most 64 KiB, one trailing newline removed);
// the second value is a usage error message, empty on success.
func readSecret(stdin io.Reader) (string, string) {
	data, err := io.ReadAll(io.LimitReader(stdin, maxSecretBytes+1))
	if err != nil {
		return "", "Cannot read secret from stdin"
	}
	if len(data) > maxSecretBytes {
		return "", "Secret exceeds 64 KiB"
	}
	secret := strings.TrimSuffix(string(data), "\r\n")
	if secret == string(data) {
		secret = strings.TrimSuffix(secret, "\n")
	}
	if secret == "" {
		return "", "Secret on stdin is empty"
	}
	return secret, ""
}

// providerFailure writes a provider error (exit 1), with the secret redacted.
func providerFailure(env Env, cmd providerCommand, err error, secret string) int {
	classified := *usage.ClassifyError(err)
	classified.Message = redact(classified.Message, secret)
	classified.Endpoint = redact(classified.Endpoint, secret)
	if cmd.json {
		return outputResult(env, usage.WriteError(env.Stdout, cmd.provider.ID(), &classified), ExitProviderError)
	}
	_, _ = fmt.Fprintf(env.Stderr, "%s: %s\n", cmd.provider.ID(), oneLine(classified.Message))
	return ExitProviderError
}

func unsupported(env Env, cmd providerCommand, what string) int {
	return usageError(env, cmd.json, fmt.Sprintf("Provider %s does not support %s", cmd.provider.ID(), what))
}

// runToken: `token set <provider>` (token on stdin) and `token delete <provider>`.
func runToken(env Env, args []string, jsonMode bool) int {
	if len(args) == 0 || (args[0] != "set" && args[0] != "delete") {
		return usageError(env, jsonMode, "token needs set or delete")
	}
	action := args[0]
	var noVerify *bool
	cmd, code, ok := parseProviderCommand(env, "token "+action, args[1:], jsonMode, 25*time.Second, 2*time.Minute, func(_ provider.Provider, fs *flag.FlagSet) {
		if action == "set" {
			noVerify = fs.Bool("no-verify", false, "Store the token without checking it with the provider (username empty)")
		}
	})
	if !ok {
		return code
	}
	manager, ok := cmd.provider.(provider.TokenManager)
	if !ok {
		return unsupported(env, cmd, "tokens")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmd.timeout)
	defer cancel()
	if action == "delete" {
		if err := manager.DeleteToken(ctx); err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return providerFailure(env, cmd, err, "")
		}
		if cmd.json {
			return outputResult(env, usage.WriteDeleted(env.Stdout, cmd.provider.ID()), ExitOK)
		}
		_, err := fmt.Fprintf(env.Stdout, "%s: token removed\n", cmd.provider.DisplayName())
		return outputResult(env, err, ExitOK)
	}
	token, message := readSecret(env.Stdin)
	if message != "" {
		return usageError(env, cmd.json, message)
	}
	username, err := manager.SetToken(ctx, token, !*noVerify)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return providerFailure(env, cmd, err, token)
	}
	return connected(env, cmd, username)
}

func connected(env Env, cmd providerCommand, username string) int {
	if cmd.json {
		return outputResult(env, usage.WriteConnected(env.Stdout, cmd.provider.ID(), username), ExitOK)
	}
	_, err := fmt.Fprintf(env.Stdout, "%s: connected as %s\n", cmd.provider.DisplayName(), username)
	return outputResult(env, err, ExitOK)
}

// runLogin: OAuth sign-in. With --json, stdout carries two documents, one per line:
// the authorize event (the URL to open) as soon as the loopback listens, then the
// result.
func runLogin(env Env, args []string, jsonMode bool) int {
	cmd, code, ok := parseProviderCommand(env, "login", args, jsonMode, 5*time.Minute, 15*time.Minute, func(p provider.Provider, fs *flag.FlagSet) {
		if l, ok := p.(provider.Loginer); ok {
			l.BindLoginFlags(fs)
		}
	})
	if !ok {
		return code
	}
	loginer, ok := cmd.provider.(provider.Loginer)
	if !ok {
		return unsupported(env, cmd, "login")
	}
	clientSecret, message := readSecret(env.Stdin)
	if message != "" {
		return usageError(env, cmd.json, "Client secret: "+message)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmd.timeout)
	defer cancel()
	announce := func(url string) error {
		if cmd.json {
			return usage.WriteAuthorize(env.Stdout, cmd.provider.ID(), url)
		}
		_, err := fmt.Fprintf(env.Stdout, "Open this URL in your browser to sign in:\n%s\n", url)
		return err
	}
	username, err := loginer.Login(ctx, clientSecret, announce)
	if err != nil {
		return providerFailure(env, cmd, err, clientSecret)
	}
	return connected(env, cmd, username)
}

// runRaw prints the provider's raw usage response, always as JSON: it is for the
// diagnostic report, which redacts it (never shown as is).
func runRaw(env Env, args []string, jsonMode bool) int {
	cmd, code, ok := parseProviderCommand(env, "raw", args, jsonMode, 25*time.Second, 2*time.Minute, nil)
	if !ok {
		return code
	}
	cmd.json = true
	reader, ok := cmd.provider.(provider.RawReader)
	if !ok {
		return unsupported(env, cmd, "raw responses")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmd.timeout)
	defer cancel()
	endpoint, raw, err := reader.RawResponse(ctx)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return providerFailure(env, cmd, err, "")
	}
	return outputResult(env, usage.WriteRaw(env.Stdout, cmd.provider.ID(), endpoint, json.RawMessage(raw)), ExitOK)
}
