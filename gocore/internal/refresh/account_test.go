package refresh_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const (
	authorizeLine = `{"schemaVersion":1,"provider":"copilot","event":"authorize","url":"https://github.com/login/oauth/authorize?client_id=Iv1.synthetic&state=synthetic"}` + "\n"
	connectedLine = `{"schemaVersion":1,"provider":"copilot","username":"octo"}` + "\n"
	deletedLine   = `{"schemaVersion":1,"provider":"copilot","deleted":true}` + "\n"
	busyLine      = `{"schemaVersion":1,"provider":"copilot","error":{"code":"unavailable","message":"Copilot OAuth: cannot start the callback server on 127.0.0.1:8123"}}` + "\n"
)

// fakeAccountCLI makes the fake CLI print out and record its arguments and
// stdin; it returns the files they land in.
func fakeAccountCLI(t *testing.T, mode, out string) (argsFile, stdinFile string) {
	t.Helper()
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out")
	if err := os.WriteFile(outFile, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	argsFile, stdinFile = filepath.Join(dir, "args"), filepath.Join(dir, "stdin")
	t.Setenv("FAKE_CLI", mode+":"+outFile)
	t.Setenv("FAKE_ARGS", argsFile)
	t.Setenv("FAKE_STDIN", stdinFile)
	return argsFile, stdinFile
}

func accountFetcher() refresh.CLIFetcher {
	return refresh.CLIFetcher{Binary: os.Args[0], TokenTimeout: 2 * time.Second, LoginTimeout: 3 * time.Second, Margin: time.Second}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var tenant = store.Account{ID: "g1", Provider: "copilot", Host: "acme.ghe.com", Scope: "organization", ManualQuota: 300}

// The token goes to the child's stdin, never its arguments, and the CLI's
// document comes back as printed.
func TestSetTokenPassesTheTokenOnStdin(t *testing.T) { //nolint:paralleltest // fakeAccountCLI sets the environment
	const token = "ghp_synthetic_token_value"
	argsFile, stdinFile := fakeAccountCLI(t, "account", connectedLine)
	doc, code, err := accountFetcher().SetToken(context.Background(), tenant, token)
	if err != nil || code != "" {
		t.Fatalf("code %q, err %v", code, err)
	}
	if string(doc) != connectedLine {
		t.Errorf("doc = %s", doc)
	}
	args := readFile(t, argsFile)
	if want := "token\nset\ncopilot\n--json\n--timeout\n2s\n--account\ng1\n--host\nacme.ghe.com"; args != want {
		t.Errorf("args =\n%s\nwant\n%s", args, want)
	}
	if strings.Contains(args, token) {
		t.Error("the token is in the arguments")
	}
	if got := readFile(t, stdinFile); got != token {
		t.Errorf("stdin = %q", got)
	}
}

func TestSetTokenReturnsTheErrorDocument(t *testing.T) { //nolint:paralleltest // fakeAccountCLI sets the environment
	line := `{"schemaVersion":1,"provider":"copilot","error":{"code":"signed_out","message":"GitHub rejected the token","reason":"token_rejected"}}` + "\n"
	fakeAccountCLI(t, "account", line)
	doc, code, err := accountFetcher().SetToken(context.Background(), tenant, "ghp_x")
	if err != nil || code != usage.CodeSignedOut || string(doc) != line {
		t.Errorf("doc %s, code %q, err %v", doc, code, err)
	}
}

func TestDeleteTokenPassesNothingOnStdin(t *testing.T) { //nolint:paralleltest // fakeAccountCLI sets the environment
	argsFile, stdinFile := fakeAccountCLI(t, "account", deletedLine)
	doc, code, err := accountFetcher().DeleteToken(context.Background(), store.Account{ID: "g2", Provider: "copilot"})
	if err != nil || code != "" || string(doc) != deletedLine {
		t.Fatalf("doc %s, code %q, err %v", doc, code, err)
	}
	if args := readFile(t, argsFile); args != "token\ndelete\ncopilot\n--json\n--timeout\n2s\n--account\ng2" {
		t.Errorf("args = %q", args)
	}
	if got := readFile(t, stdinFile); got != "" {
		t.Errorf("stdin = %q", got)
	}
}

func TestAccountCommandsRejectABadDocument(t *testing.T) { //nolint:paralleltest // fakeAccountCLI sets the environment
	fakeAccountCLI(t, "account", `{"schemaVersion":1,"provider":"copilot","usage":{}}`+"\n")
	if _, _, err := accountFetcher().SetToken(context.Background(), tenant, "ghp_x"); !errors.Is(err, usage.ErrBadDocument) {
		t.Errorf("err = %v", err)
	}
	if _, _, err := accountFetcher().DeleteToken(context.Background(), tenant); !errors.Is(err, usage.ErrBadDocument) {
		t.Errorf("err = %v", err)
	}
}

func TestAccountCommandsAreCopilotOnly(t *testing.T) {
	t.Parallel()
	claude := store.Account{ID: "c", Provider: "claude"}
	if _, _, err := accountFetcher().SetToken(context.Background(), claude, "x"); err == nil {
		t.Error("token set on claude")
	}
	if _, _, err := accountFetcher().DeleteToken(context.Background(), claude); err == nil {
		t.Error("token delete on claude")
	}
	if _, _, err := accountFetcher().Login(context.Background(), claude, refresh.LoginRequest{ClientID: "a", ClientSecret: "b"}, func([]byte) {}); err == nil {
		t.Error("login on claude")
	}
}

// The login announces the authorize event as soon as the child prints it,
// then returns the result document; the client secret goes on stdin.
func TestLoginAnnouncesTheURLThenReturnsTheResult(t *testing.T) { //nolint:paralleltest // fakeAccountCLI sets the environment
	const secret = "synthetic-client-secret"
	argsFile, stdinFile := fakeAccountCLI(t, "account", authorizeLine+connectedLine)
	var announced []string
	doc, code, err := accountFetcher().Login(context.Background(), tenant,
		refresh.LoginRequest{ClientID: "Iv1.synthetic", ClientSecret: secret, Locale: "it"},
		func(d []byte) { announced = append(announced, string(d)) })
	if err != nil || code != "" || string(doc) != connectedLine {
		t.Fatalf("doc %s, code %q, err %v", doc, code, err)
	}
	if len(announced) != 1 || announced[0] != authorizeLine {
		t.Errorf("announced = %q", announced)
	}
	args := readFile(t, argsFile)
	want := "login\ncopilot\n--json\n--timeout\n3s\n--account\ng1\n--host\nacme.ghe.com\n--client-id=Iv1.synthetic\n--locale\nit"
	if args != want {
		t.Errorf("args =\n%s\nwant\n%s", args, want)
	}
	if strings.Contains(args, secret) {
		t.Error("the client secret is in the arguments")
	}
	if got := readFile(t, stdinFile); got != secret {
		t.Errorf("stdin = %q", got)
	}
}

func TestLoginDefaultsToEnglish(t *testing.T) { //nolint:paralleltest // fakeAccountCLI sets the environment
	argsFile, _ := fakeAccountCLI(t, "account", authorizeLine+connectedLine)
	if _, _, err := accountFetcher().Login(context.Background(), store.Account{ID: "g", Provider: "copilot"},
		refresh.LoginRequest{ClientID: "id", ClientSecret: "s"}, func([]byte) {}); err != nil {
		t.Fatal(err)
	}
	if args := readFile(t, argsFile); !strings.HasSuffix(args, "--client-id=id\n--locale\nen") || strings.Contains(args, "--host") {
		t.Errorf("args = %q", args)
	}
}

// A login that fails before announcing (port 8123 busy) announces nothing.
func TestLoginFailingBeforeTheURL(t *testing.T) { //nolint:paralleltest // fakeAccountCLI sets the environment
	fakeAccountCLI(t, "account", busyLine)
	called := false
	doc, code, err := accountFetcher().Login(context.Background(), tenant,
		refresh.LoginRequest{ClientID: "id", ClientSecret: "s"}, func([]byte) { called = true })
	if err != nil || code != usage.CodeUnavailable || string(doc) != busyLine || called {
		t.Errorf("doc %s, code %q, err %v, announced %v", doc, code, err, called)
	}
}

// Cancelling a login that waits for the browser ends the child.
func TestLoginCancelled(t *testing.T) { //nolint:paralleltest // fakeAccountCLI sets the environment
	fakeAccountCLI(t, "login-hang", authorizeLine)
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	announced := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, _, err := accountFetcher().Login(ctx, tenant, refresh.LoginRequest{ClientID: "id", ClientSecret: "s"},
			func([]byte) { once.Do(func() { close(announced) }) })
		done <- err
	}()
	select {
	case <-announced:
	case <-time.After(5 * time.Second):
		t.Fatal("no authorize event")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the login did not end")
	}
}

func TestLoginWithOnlyTheAuthorizeEventIsABadDocument(t *testing.T) { //nolint:paralleltest // fakeAccountCLI sets the environment
	fakeAccountCLI(t, "account", authorizeLine)
	_, _, err := accountFetcher().Login(context.Background(), tenant, refresh.LoginRequest{ClientID: "id", ClientSecret: "s"}, func([]byte) {})
	if !errors.Is(err, usage.ErrBadDocument) {
		t.Errorf("err = %v", err)
	}
}
