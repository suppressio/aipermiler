// Package copilot reads GitHub Copilot usage (port of services/copilot.ts) and owns the
// account token: it lives in the OS keyring under the account id (decided on #179),
// written by `token set` (a PAT on stdin) or `login` (OAuth App, PKCE loopback, port of
// main/copilot-oauth.ts) and read by `usage`. The token never crosses argv or the
// JSON documents.
package copilot

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
	"github.com/suppressio/aipermiler/gocore/internal/github"
	"github.com/suppressio/aipermiler/gocore/internal/secrets"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// Provider is one Copilot account of one run.
type Provider struct {
	account     string
	host        string
	scope       string
	manualQuota string

	clientID string
	locale   string

	store  secrets.Store
	client *github.Client
	now    func() time.Time
	// endpoints overrides the host's base URLs (tests).
	endpoints *github.Endpoints
	// listenAddr is the OAuth loopback address (tests use port 0).
	listenAddr string
}

// New creates a provider that keeps tokens in store.
func New(store secrets.Store) *Provider {
	return &Provider{
		store:      store,
		client:     github.NewClient(buildinfo.Name + "/" + buildinfo.Version),
		now:        time.Now,
		listenAddr: "127.0.0.1:8123",
	}
}

// ID is the stable provider identifier.
func (*Provider) ID() string { return "copilot" }

// DisplayName is the terminal display name.
func (*Provider) DisplayName() string { return "Copilot" }

// AcceptsSecret is false for usage: the token comes from the keyring, not stdin.
func (*Provider) AcceptsSecret() bool { return false }

// BindFlags adds the account parameters (never the token).
func (p *Provider) BindFlags(fs *flag.FlagSet) {
	fs.StringVar(&p.account, "account", "", "Account id: names the token in the OS keyring (required)")
	fs.StringVar(&p.host, "host", github.DefaultHost, "github.com or <tenant>.ghe.com")
	fs.StringVar(&p.scope, "scope", "personal", "personal (plan) or organization (company seat)")
	fs.StringVar(&p.manualQuota, "manual-quota", "", "Monthly AI credits of the plan, total of the billing-report window (personal scope)")
}

// BindLoginFlags adds the OAuth App parameters of `login`.
func (p *Provider) BindLoginFlags(fs *flag.FlagSet) {
	fs.StringVar(&p.clientID, "client-id", "", "Client ID of the user's GitHub OAuth App (required)")
	fs.StringVar(&p.locale, "locale", "en", "Language of the browser page after sign-in: en or it")
}

// Validate checks the flags before any keyring or network access.
func (p *Provider) Validate() error {
	if !secrets.ValidAccount(p.account) {
		return errors.New("--account is required: letters, digits, '.', '_' or '-', at most 128")
	}
	if github.NormalizeHost(p.host) == "" {
		return fmt.Errorf("--host %q is not supported: github.com or <tenant>.ghe.com", p.host)
	}
	if p.scope != "personal" && p.scope != "organization" {
		return errors.New("--scope must be personal or organization")
	}
	if p.manualQuota != "" {
		if q, err := strconv.ParseFloat(p.manualQuota, 64); err != nil || q < 0 || math.IsNaN(q) || math.IsInf(q, 0) {
			return errors.New("--manual-quota must be a finite number ≥ 0")
		}
	}
	return nil
}

func (p *Provider) apiEndpoints() github.Endpoints {
	if p.endpoints != nil {
		return *p.endpoints
	}
	e, _ := github.EndpointsFor(p.host) // checked by Validate
	return e
}

func (p *Provider) token(ctx context.Context) (string, error) {
	token, err := secrets.Get(ctx, p.store, secrets.CopilotKey(p.account))
	if errors.Is(err, secrets.ErrNotFound) {
		return "", &usage.Error{Code: usage.CodeSignedOut, Reason: ReasonNotConnected, Message: "Copilot: account not connected — connect it from Settings"}
	}
	if err != nil {
		return "", keyringError(err)
	}
	return token, nil
}

// keyringError: an unreachable keyring, or one that did not answer in time (a pending
// unlock prompt) — then the deadline wins and the error is a timeout.
func keyringError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &usage.Error{Code: usage.CodeUnavailable, Reason: ReasonKeyringUnavailable, Message: "Copilot: the OS keyring is not available: " + err.Error()}
}

// Fetch reads the usage with the stored token.
func (p *Provider) Fetch(ctx context.Context, _ string) (usage.Usage, error) {
	token, err := p.token(ctx)
	if err != nil {
		return usage.Usage{}, err
	}
	f := &fetcher{client: p.client, api: p.apiEndpoints().API, token: token, now: p.now(), personal: p.scope == "personal"}
	if p.manualQuota != "" {
		q, _ := strconv.ParseFloat(p.manualQuota, 64) // checked by Validate
		f.manual = &q
	}
	result, err := f.fetch(ctx)
	if err != nil {
		return usage.Usage{}, classify(err, token)
	}
	return result, nil
}

// classify maps a fetch failure to the contract: a 401 is a token GitHub no longer
// accepts (reconnect); the rest keep their usage.Error or become unavailable.
func classify(err error, token string) error {
	var known *usage.Error
	if errors.As(err, &known) {
		return known
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	message := strings.ReplaceAll(err.Error(), token, "[redacted]")
	if github.StatusOf(err) == http.StatusUnauthorized {
		return &usage.Error{Code: usage.CodeSignedOut, Reason: ReasonTokenRejected, Message: "Copilot: GitHub rejected the token, reconnect the account (" + message + ")"}
	}
	return &usage.Error{Code: usage.CodeUnavailable, Message: message}
}

// RawResponse is the copilot_internal/user answer, for the diagnostic report (the
// client redacts it); the main source for both scopes.
func (p *Provider) RawResponse(ctx context.Context) (string, []byte, error) {
	token, err := p.token(ctx)
	if err != nil {
		return "", nil, err
	}
	f := &fetcher{client: p.client, api: p.apiEndpoints().API, token: token}
	raw, err := f.internalRaw(ctx)
	if err != nil {
		return "", nil, classify(err, token)
	}
	return InternalEndpointLabel, raw, nil
}

// SetToken checks a token with GitHub (GET /user) and stores it: a PAT pasted in
// Settings. verify false stores it unchecked: the token Electron kept before #179,
// moved at startup without a network call (it was already checked when connected).
func (p *Provider) SetToken(ctx context.Context, token string, verify bool) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("empty token")
	}
	username := ""
	if verify {
		var err error
		if username, err = Username(ctx, p.client, p.apiEndpoints().API, token); err != nil {
			return "", classify(err, token)
		}
	}
	if err := secrets.Set(ctx, p.store, secrets.CopilotKey(p.account), token); err != nil {
		return "", keyringError(err)
	}
	return username, nil
}

// DeleteToken removes the account's token (disconnect, account removal).
func (p *Provider) DeleteToken(ctx context.Context) error {
	if err := secrets.Delete(ctx, p.store, secrets.CopilotKey(p.account)); err != nil {
		return keyringError(err)
	}
	return nil
}
