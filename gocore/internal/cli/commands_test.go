package cli_test

import (
	"context"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/cli"
	"github.com/suppressio/aipermiler/gocore/internal/provider"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// accountProvider is a fake with every optional capability (tokens, login, raw,
// validation), like Copilot.
type accountProvider struct {
	fakeProvider
	clientID  string
	tokens    map[string]string
	gotSecret string
	err       error
}

func (p *accountProvider) BindLoginFlags(fs *flag.FlagSet) {
	fs.StringVar(&p.clientID, "client-id", "", "Client ID")
}

func (p *accountProvider) Validate() error {
	if p.account == "" {
		return errors.New("--account is required")
	}
	return nil
}

func (p *accountProvider) SetToken(_ context.Context, token string, verify bool) (string, error) {
	p.gotSecret = token
	if p.err != nil {
		return "", p.err
	}
	p.tokens[p.account] = token
	if !verify {
		return "", nil
	}
	return "octo", nil
}

func (p *accountProvider) DeleteToken(context.Context) error {
	delete(p.tokens, p.account)
	return p.err
}

func (p *accountProvider) Login(_ context.Context, secret string, announce func(string) error) (string, error) {
	p.gotSecret = secret
	if err := announce("https://github.com/login/oauth/authorize?client_id=" + p.clientID + "&state=synthetic"); err != nil {
		return "", err
	}
	if p.err != nil {
		return "", p.err
	}
	return "octo", nil
}

func (p *accountProvider) RawResponse(context.Context) (string, []byte, error) {
	if p.err != nil {
		return "", nil, p.err
	}
	return "{api}/fake", []byte(`{"z":1,"a":[true,null]}`), nil
}

func accountFactory(p *accountProvider) func() provider.Provider {
	if p.tokens == nil {
		p.tokens = map[string]string{}
	}
	return func() provider.Provider { return p }
}

func TestAccountCommandFixtures(t *testing.T) {
	t.Parallel()
	t.Run("token set prints account-connected, the token read from stdin", func(t *testing.T) {
		t.Parallel()
		p := &accountProvider{}
		code, out, errOut := run(t, []string{"token", "set", "fake", "--account", "a1", "--json"}, strings.NewReader("synthetic-pat\n"), accountFactory(p))
		if code != cli.ExitOK || out != string(fixture(t, "account-connected")) || p.tokens["a1"] != "synthetic-pat" {
			t.Fatalf("exit=%d out=%q err=%q tokens=%v", code, out, errOut, p.tokens)
		}
	})
	t.Run("token set --no-verify stores without a user name", func(t *testing.T) {
		t.Parallel()
		p := &accountProvider{}
		code, out, _ := run(t, []string{"token", "set", "fake", "--account", "a1", "--no-verify", "--json"}, strings.NewReader("legacy"), accountFactory(p))
		if code != cli.ExitOK || out != `{"schemaVersion":1,"provider":"fake","username":""}`+"\n" || p.tokens["a1"] != "legacy" {
			t.Fatalf("exit=%d out=%q", code, out)
		}
	})
	t.Run("token delete prints account-deleted", func(t *testing.T) {
		t.Parallel()
		p := &accountProvider{tokens: map[string]string{"a1": "x"}}
		code, out, _ := run(t, []string{"token", "delete", "fake", "--account", "a1", "--json"}, strings.NewReader(""), accountFactory(p))
		if code != cli.ExitOK || out != string(fixture(t, "account-deleted")) || len(p.tokens) != 0 {
			t.Fatalf("exit=%d out=%q tokens=%v", code, out, p.tokens)
		}
	})
	t.Run("login prints the authorize event, then account-connected", func(t *testing.T) {
		t.Parallel()
		p := &accountProvider{}
		code, out, _ := run(t, []string{"login", "fake", "--account", "a1", "--client-id", "Iv1.synthetic", "--json"}, strings.NewReader("synthetic-client-secret"), accountFactory(p))
		want := string(fixture(t, "login-authorize")) + string(fixture(t, "account-connected"))
		if code != cli.ExitOK || out != want || p.gotSecret != "synthetic-client-secret" {
			t.Fatalf("exit=%d out=%q", code, out)
		}
	})
	t.Run("raw prints the raw document, JSON even without --json", func(t *testing.T) {
		t.Parallel()
		code, out, _ := run(t, []string{"raw", "fake", "--account", "a1"}, strings.NewReader(""), accountFactory(&accountProvider{}))
		if code != cli.ExitOK || out != string(fixture(t, "raw")) {
			t.Fatalf("exit=%d out=%q", code, out)
		}
	})
	t.Run("an error with a reason", func(t *testing.T) {
		t.Parallel()
		p := &accountProvider{err: &usage.Error{Code: usage.CodeUnavailable, Reason: "enterprise_managed_seat", Message: "GitHub exposes no usage data for this seat"}}
		code, out, _ := run(t, []string{"raw", "fake", "--account", "a1", "--json"}, strings.NewReader(""), accountFactory(p))
		if code != cli.ExitProviderError || out != string(fixture(t, "error-unavailable-reason")) {
			t.Fatalf("exit=%d out=%q", code, out)
		}
	})
}

func TestAccountCommandErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		args  []string
		stdin string
		p     *accountProvider
		exit  int
		check func(t *testing.T, out, errOut string, p *accountProvider)
	}{
		{"token without action", []string{"token", "fake", "--json"}, "", &accountProvider{}, cli.ExitUsage, nil},
		{"token set without a token on stdin", []string{"token", "set", "fake", "--account", "a1", "--json"}, "", &accountProvider{}, cli.ExitUsage, nil},
		{"validation failure is a usage error", []string{"token", "set", "fake", "--json"}, "t", &accountProvider{}, cli.ExitUsage, func(t *testing.T, out, _ string, p *accountProvider) {
			t.Helper()
			if !strings.Contains(out, "--account is required") || p.gotSecret != "" {
				t.Errorf("out=%q secret read=%q", out, p.gotSecret)
			}
		}},
		{"a provider error never quotes the token", []string{"token", "set", "fake", "--account", "a1", "--json"}, "synthetic-pat", &accountProvider{err: errors.New("GitHub rejected synthetic-pat")}, cli.ExitProviderError, func(t *testing.T, out, errOut string, _ *accountProvider) {
			t.Helper()
			if strings.Contains(out+errOut, "synthetic-pat") || !strings.Contains(out, "[redacted]") {
				t.Errorf("out=%q", out)
			}
		}},
		{"login without the client secret", []string{"login", "fake", "--account", "a1", "--json"}, "", &accountProvider{}, cli.ExitUsage, nil},
		{"login timeout above the maximum", []string{"login", "fake", "--account", "a1", "--timeout", "1h", "--json"}, "s", &accountProvider{}, cli.ExitUsage, nil},
		{"login text mode prints the URL to open", []string{"login", "fake", "--account", "a1", "--client-id", "c"}, "s", &accountProvider{}, cli.ExitOK, func(t *testing.T, out, _ string, _ *accountProvider) {
			t.Helper()
			if !strings.Contains(out, "Open this URL") || !strings.Contains(out, "client_id=c") || !strings.Contains(out, "Fake: connected as octo") {
				t.Errorf("out=%q", out)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, out, errOut := run(t, tt.args, strings.NewReader(tt.stdin), accountFactory(tt.p))
			if code != tt.exit {
				t.Fatalf("exit=%d; want %d (out=%q err=%q)", code, tt.exit, out, errOut)
			}
			if tt.check != nil {
				tt.check(t, out, errOut, tt.p)
			}
		})
	}
}

func TestCommandsUnsupportedByProvider(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"token", "set", "fake"}, {"login", "fake"}, {"raw", "fake"}} {
		code, out, _ := run(t, append(args, "--json"), strings.NewReader("x"), standardFake)
		if code != cli.ExitUsage || !strings.Contains(out, "does not support") {
			t.Errorf("%v: exit=%d out=%q", args, code, out)
		}
	}
}
