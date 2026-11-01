// Package provider defines factories for independently configured usage providers.
package provider

import (
	"context"
	"flag"

	"github.com/suppressio/aipermiler/gocore/internal/codex"
	"github.com/suppressio/aipermiler/gocore/internal/copilot"
	"github.com/suppressio/aipermiler/gocore/internal/secrets"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// Provider binds account flags and fetches usage without putting secrets in argv.
type Provider interface {
	ID() string
	DisplayName() string
	BindFlags(fs *flag.FlagSet)
	AcceptsSecret() bool
	Fetch(ctx context.Context, secret string) (usage.Usage, error)
}

// Validator is implemented by providers whose flags need checking after parsing: a
// failure is a usage error (exit 2), before any keyring or network access.
type Validator interface {
	Validate() error
}

// TokenManager keeps the account's token in the OS keyring (`token set|delete`).
type TokenManager interface {
	// SetToken checks the token with the provider (unless verify is false), stores it
	// and returns the user name ("" when not verified).
	SetToken(ctx context.Context, token string, verify bool) (string, error)
	DeleteToken(ctx context.Context) error
}

// Loginer signs in through the provider's own OAuth flow (`login`).
type Loginer interface {
	BindLoginFlags(fs *flag.FlagSet)
	// Login gives announce the URL to open, waits for the sign-in, stores the token and
	// returns the user name. secret is the OAuth App client secret, read from stdin.
	Login(ctx context.Context, secret string, announce func(url string) error) (string, error)
}

// RawReader returns the provider's raw usage response for the diagnostic report.
type RawReader interface {
	RawResponse(ctx context.Context) (endpoint string, raw []byte, err error)
}

// Registry creates a fresh provider instance for each CLI run.
type Registry map[string]func() Provider

// Default returns the real binary's providers.
func Default() Registry {
	return Registry{
		"codex":   func() Provider { return codex.New() },
		"copilot": func() Provider { return copilot.New(secrets.OSKeyring{}) },
	}
}
