package refresh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// The account commands of the CLI (`token set|delete`, `login`), run for the
// service's account endpoints (#285) like the reads: as a child of the same
// binary. Secrets (the token, the OAuth client secret) go to the child's
// stdin, never its arguments; the child alone talks to GitHub and the keyring
// (#179), so the token GitHub hands out at login never reaches this process.

const (
	// DefaultTokenTimeout is the CLI's own default for `token`.
	DefaultTokenTimeout = 25 * time.Second
	// DefaultLoginTimeout is the CLI's own default for `login`: the browser
	// round trip.
	DefaultLoginTimeout = 5 * time.Minute
)

// LoginRequest is what a login needs besides the account. ClientSecret goes
// to the child's stdin; Locale "" means en.
type LoginRequest struct {
	ClientID, ClientSecret, Locale string
}

// SetToken runs `token set <provider> --json` with the account's flags and
// token on stdin, and returns the CLI's document as printed (`username`, or
// an error document with its code). Errors are those of Raw.
func (f CLIFetcher) SetToken(ctx context.Context, account store.Account, token string) ([]byte, usage.Code, error) {
	return f.tokenCommand(ctx, account, "set", token, "username")
}

// DeleteToken runs `token delete <provider> --json` with the account's flags
// and returns the CLI's document (`deleted`, or an error document).
func (f CLIFetcher) DeleteToken(ctx context.Context, account store.Account) ([]byte, usage.Code, error) {
	return f.tokenCommand(ctx, account, "delete", "", "deleted")
}

func (f CLIFetcher) tokenCommand(ctx context.Context, account store.Account, action, secret, field string) ([]byte, usage.Code, error) {
	timeout := orDefault(f.TokenTimeout, DefaultTokenTimeout)
	args, err := accountArgs(account, timeout, "token", action)
	if err != nil {
		return nil, "", err
	}
	out, err := f.run(ctx, args, timeout, secret, nil)
	if err != nil {
		return nil, "", err
	}
	return accountDocument(out, out.stdout, field)
}

// Login runs `login <provider> --json` with the client secret on stdin.
// authorize gets the CLI's authorize event, as printed, as soon as the child
// prints it (at most once, from another goroutine); Login then waits for the
// result document (`username`, or an error document) and returns it. A
// cancelled ctx ends the child and returns ctx's error.
func (f CLIFetcher) Login(ctx context.Context, account store.Account, req LoginRequest, authorize func([]byte)) ([]byte, usage.Code, error) {
	timeout := orDefault(f.LoginTimeout, DefaultLoginTimeout)
	args, err := accountArgs(account, timeout, "login")
	if err != nil {
		return nil, "", err
	}
	locale := req.Locale
	if locale == "" {
		locale = "en"
	}
	// --client-id=<id> in one argument: an id starting with "-" stays a value.
	args = append(args, "--client-id="+req.ClientID, "--locale", locale)
	announced := false
	out, err := f.run(ctx, args, timeout, req.ClientSecret, func(line []byte) {
		if !announced && isAuthorizeEvent(line) {
			announced = true
			authorize(line)
		}
	})
	if err != nil {
		return nil, "", err
	}
	var result []byte
	for line := range bytes.Lines(out.stdout) {
		if len(bytes.TrimSpace(line)) == 0 || isAuthorizeEvent(line) {
			continue
		}
		if result != nil {
			return nil, "", fmt.Errorf("%w: more than one login result", usage.ErrBadDocument)
		}
		result = line
	}
	return accountDocument(out, result, "username")
}

// accountArgs are the CLI arguments of an account command: the account and
// its host, the only flags the keyring entry and GitHub's address depend on.
func accountArgs(account store.Account, timeout time.Duration, command ...string) ([]string, error) {
	if account.Provider != "copilot" {
		return nil, fmt.Errorf("provider %q has no account commands", account.Provider)
	}
	args := append(append([]string{}, command...), account.Provider, "--json", "--timeout", timeout.String(), "--account", account.ID)
	if account.Host != "" {
		args = append(args, "--host", account.Host)
	}
	return args, nil
}

func isAuthorizeEvent(line []byte) bool {
	var doc struct {
		Event string `json:"event"`
		URL   string `json:"url"`
	}
	return json.Unmarshal(line, &doc) == nil && doc.Event == "authorize" && doc.URL != ""
}

// accountDocument checks that doc is one v1 document of an account command:
// an error document (returned with its code) or one carrying field.
func accountDocument(out childOutput, doc []byte, field string) ([]byte, usage.Code, error) {
	var wire struct {
		SchemaVersion int `json:"schemaVersion"`
		Error         *struct {
			Code usage.Code `json:"code"`
		} `json:"error"`
	}
	var fields map[string]json.RawMessage
	var err error
	switch {
	case doc == nil:
		err = fmt.Errorf("%w: no document", usage.ErrBadDocument)
	case json.Unmarshal(doc, &wire) != nil || json.Unmarshal(doc, &fields) != nil:
		err = fmt.Errorf("%w: output is not one JSON document", usage.ErrBadDocument)
	case wire.SchemaVersion != usage.SchemaVersion:
		err = fmt.Errorf("%w: schemaVersion %d", usage.ErrBadDocument, wire.SchemaVersion)
	case wire.Error != nil && wire.Error.Code != "":
		return doc, wire.Error.Code, nil
	case fields[field] == nil:
		err = fmt.Errorf("%w: no %s", usage.ErrBadDocument, field)
	default:
		return doc, "", nil
	}
	if out.exitErr != nil {
		err = fmt.Errorf("%w (core CLI: %w)", err, out.exitErr)
	}
	return nil, "", err
}
