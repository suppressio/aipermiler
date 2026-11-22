// Package refresh is the service's refresh loop (#283): it reads the accounts
// through the core's own CLI, records every fresh value in the history and
// commits it to the store.
package refresh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const (
	// DefaultCLITimeout is the deadline the CLI gets for one read, as Electron
	// passes it today (main/coreClient.ts).
	DefaultCLITimeout = 120 * time.Second
	// DefaultRawTimeout is the CLI's own default for `raw`.
	DefaultRawTimeout = 25 * time.Second
	// DefaultMargin is how much longer the service waits for the child than
	// the CLI's own deadline, so the CLI reports its own timeout (#266).
	DefaultMargin = 15 * time.Second
	// stopGrace is how long a cancelled child gets to end its own tree.
	stopGrace = 5 * time.Second
	// maxOutput bounds what is kept of the child's stdout, as Electron's
	// maxBuffer does (main/coreClient.ts): a document is a few KiB.
	maxOutput = 1 << 20
)

// ErrChildTimeout means the CLI did not answer even after its own deadline
// plus the margin.
var ErrChildTimeout = errors.New("core CLI did not answer in time")

// CLIFetcher reads an account by running the core's binary (`usage <provider>
// --json`): a hang or crash in a provider adapter ends the child, not the
// service (#246, decision 1). No secret goes to the child: the Copilot token
// is in the keyring the child reads itself (#179).
type CLIFetcher struct {
	Binary  string        // the core's own executable (os.Executable)
	Timeout time.Duration // the CLI's --timeout of a usage read; zero means DefaultCLITimeout
	// RawTimeout is the CLI's --timeout of a raw read (at most 2m, the CLI's
	// cap); zero means DefaultRawTimeout.
	RawTimeout time.Duration
	Margin     time.Duration // zero means DefaultMargin
}

// Fetch returns fresh values, or an error: a *usage.Error for an error
// document or for last known values (stale, which are not fresh),
// usage.ErrBadDocument, ErrChildTimeout, the context's error, or the error of
// starting the child.
func (f CLIFetcher) Fetch(ctx context.Context, account store.Account) (usage.Usage, error) {
	timeout := orDefault(f.Timeout, DefaultCLITimeout)
	args, err := UsageArgs(account, timeout)
	if err != nil {
		return usage.Usage{}, err
	}
	out, err := f.run(ctx, args, timeout)
	if err != nil {
		return usage.Usage{}, err
	}
	// The exit code is not read: a provider failure exits non-zero with an
	// error document, and anything else fails to parse.
	doc, err := usage.ParseDocument(out.stdout)
	if err != nil {
		if out.exitErr != nil {
			return usage.Usage{}, fmt.Errorf("%w (core CLI: %w)", err, out.exitErr)
		}
		return usage.Usage{}, err
	}
	if doc.Err != nil {
		return usage.Usage{}, doc.Err
	}
	if doc.Usage.Stale != nil {
		return usage.Usage{}, &usage.Error{Code: doc.Usage.Stale.Code, Message: doc.Usage.Stale.Message, Reason: doc.Usage.Stale.Reason}
	}
	return doc.Usage, nil
}

// Raw runs `raw <provider> --json` for the diagnostic report (#248) and returns
// the CLI's document as it printed it, a raw document or an error document,
// with the error's code ("" for a raw document). Errors are those of Fetch
// except *usage.Error: an error document is a document, returned as such.
func (f CLIFetcher) Raw(ctx context.Context, account store.Account) ([]byte, usage.Code, error) {
	timeout := orDefault(f.RawTimeout, DefaultRawTimeout)
	args, err := RawArgs(account, timeout)
	if err != nil {
		return nil, "", err
	}
	out, err := f.run(ctx, args, timeout)
	if err != nil {
		return nil, "", err
	}
	var doc struct {
		SchemaVersion int             `json:"schemaVersion"`
		Response      json.RawMessage `json:"response"`
		Error         *struct {
			Code usage.Code `json:"code"`
		} `json:"error"`
	}
	switch {
	case json.Unmarshal(out.stdout, &doc) != nil:
		err = fmt.Errorf("%w: raw output is not JSON", usage.ErrBadDocument)
	case doc.SchemaVersion != usage.SchemaVersion:
		err = fmt.Errorf("%w: schemaVersion %d", usage.ErrBadDocument, doc.SchemaVersion)
	case doc.Error != nil && doc.Error.Code != "":
		return out.stdout, doc.Error.Code, nil
	case doc.Response == nil:
		err = fmt.Errorf("%w: neither a raw nor an error document", usage.ErrBadDocument)
	default:
		return out.stdout, "", nil
	}
	if out.exitErr != nil {
		err = fmt.Errorf("%w (core CLI: %w)", err, out.exitErr)
	}
	return nil, "", err
}

func orDefault(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}

// childOutput is what a finished child printed, with the error of Wait (a
// non-zero exit is normal: the output says what happened).
type childOutput struct {
	stdout  []byte
	exitErr error
}

// run starts the core CLI with args. The error is set only when there is no
// output to read: the context ended, the output went over the limit, the child
// outlived timeout plus the margin, or it did not start.
func (f CLIFetcher) run(ctx context.Context, args []string, timeout time.Duration) (childOutput, error) {
	margin := f.Margin
	if margin <= 0 {
		margin = DefaultMargin
	}
	childCtx, cancel := context.WithTimeout(ctx, timeout+margin)
	defer cancel()
	cmd := exec.CommandContext(childCtx, f.Binary, args...) //nolint:gosec // the core's own binary, args built here
	stdout := &limitedBuffer{max: maxOutput, over: cancel}
	cmd.Stdout = stdout
	cmd.WaitDelay = stopGrace
	tree := newProcessTree(cmd)
	if err := cmd.Start(); err != nil {
		return childOutput{}, fmt.Errorf("start core CLI: %w", err)
	}
	tree.started()
	runErr := cmd.Wait()
	tree.sweep()
	switch {
	case ctx.Err() != nil:
		return childOutput{}, ctx.Err()
	case stdout.exceeded:
		return childOutput{}, fmt.Errorf("%w: output over %d bytes", usage.ErrBadDocument, maxOutput)
	case childCtx.Err() != nil:
		return childOutput{}, fmt.Errorf("%w (%s)", ErrChildTimeout, timeout+margin)
	}
	return childOutput{stdout.buf.Bytes(), runErr}, nil
}

// UsageArgs are the CLI arguments that read account: the same flags Electron
// passes today (main/coreRequests.ts).
func UsageArgs(account store.Account, timeout time.Duration) ([]string, error) {
	args := []string{"usage", account.Provider, "--json", "--timeout", timeout.String()}
	switch account.Provider {
	case "claude", "codex":
		if account.BinaryPath != nil && *account.BinaryPath != "" {
			args = append(args, "--"+account.Provider+"-bin", *account.BinaryPath)
		}
	case "copilot":
		args = append(args, "--account", account.ID)
		if account.Host != "" {
			args = append(args, "--host", account.Host)
		}
		if account.Scope != "" {
			args = append(args, "--scope", account.Scope)
		}
		if account.Scope == "" || account.Scope == "personal" {
			args = append(args, "--manual-quota", strconv.FormatFloat(account.ManualQuota, 'f', -1, 64))
		}
	default:
		return nil, fmt.Errorf("unknown provider %q", account.Provider)
	}
	return args, nil
}

// RawArgs are the CLI arguments of the diagnostic read: the provider and, for
// Copilot, the account parameters the raw response depends on.
func RawArgs(account store.Account, timeout time.Duration) ([]string, error) {
	args := []string{"raw", account.Provider, "--json", "--timeout", timeout.String()}
	switch account.Provider {
	case "claude", "codex":
	case "copilot":
		args = append(args, "--account", account.ID)
		if account.Host != "" {
			args = append(args, "--host", account.Host)
		}
		if account.Scope != "" {
			args = append(args, "--scope", account.Scope)
		}
	default:
		return nil, fmt.Errorf("unknown provider %q", account.Provider)
	}
	return args, nil
}

// limitedBuffer keeps up to max bytes; past that it drops the rest and calls
// over, which stops the child. Written only by exec's copying goroutine and
// read after Wait.
type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	over     func()
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if !b.exceeded && b.buf.Len()+len(p) > b.max {
		b.exceeded = true
		b.over()
	}
	if !b.exceeded {
		b.buf.Write(p)
	}
	return len(p), nil
}
