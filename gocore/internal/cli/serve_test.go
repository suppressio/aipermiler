package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/cli"
	"github.com/suppressio/aipermiler/gocore/internal/provider"
)

type served struct {
	cancel context.CancelFunc
	exited chan struct{}
	code   int
	stderr *bytes.Buffer
}

func startServe(t *testing.T) *served {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	s := &served{cancel: cancel, exited: make(chan struct{}), stderr: new(bytes.Buffer)}
	env := cli.Env{Providers: provider.Registry{}, Stdin: strings.NewReader(""), Stdout: new(bytes.Buffer), Stderr: s.stderr, Context: ctx}
	go func() { s.code = cli.RunWith(env, []string{"serve"}); close(s.exited) }()
	t.Cleanup(func() {
		cancel()
		<-s.exited // the instance must be gone before the temp dir is removed
	})
	return s
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not appear", path)
}

func TestServeRunsUntilCancelled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIPERMILER_CONFIG_DIR", dir)
	s := startServe(t)
	waitForFile(t, filepath.Join(dir, "api.json"))
	s.cancel()
	select {
	case <-s.exited:
		if s.code != cli.ExitOK {
			t.Fatalf("exit = %d; want 0 (stderr: %s)", s.code, s.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop")
	}
	if _, err := os.Stat(filepath.Join(dir, "api.json")); !os.IsNotExist(err) {
		t.Errorf("api.json survived: %v", err)
	}
}

func TestServeSecondInstanceExitsWithItsOwnCode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIPERMILER_CONFIG_DIR", dir)
	startServe(t)
	waitForFile(t, filepath.Join(dir, "api.json"))

	var stderr bytes.Buffer
	env := cli.Env{Providers: provider.Registry{}, Stdin: strings.NewReader(""), Stdout: new(bytes.Buffer), Stderr: &stderr, Context: t.Context()}
	code := cli.RunWith(env, []string{"serve"})
	if code != cli.ExitAlreadyRunning || cli.ExitAlreadyRunning == cli.ExitOK || cli.ExitAlreadyRunning == cli.ExitProviderError || cli.ExitAlreadyRunning == cli.ExitUsage {
		t.Fatalf("exit = %d, ExitAlreadyRunning = %d; want an exit code of its own", code, cli.ExitAlreadyRunning)
	}
	if !strings.Contains(stderr.String(), "already running") {
		t.Errorf("stderr = %q; want it to say already running", stderr.String())
	}
}

func TestServeRejectsArguments(t *testing.T) {
	t.Setenv("AIPERMILER_CONFIG_DIR", t.TempDir())
	for _, args := range [][]string{{"extra"}, {"--port", "80"}, {"--json"}} {
		var stderr bytes.Buffer
		env := cli.Env{Providers: provider.Registry{}, Stdin: strings.NewReader(""), Stdout: new(bytes.Buffer), Stderr: &stderr, Context: t.Context()}
		if code := cli.RunWith(env, append([]string{"serve"}, args...)); code != cli.ExitUsage {
			t.Errorf("serve %v exit = %d; want %d (stderr %q)", args, code, cli.ExitUsage, stderr.String())
		}
	}
}

// The invalid-level warning comes from RunWith's logger; serve must reuse it.
func TestServeWarnsOnceAboutInvalidLogLevel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIPERMILER_CONFIG_DIR", dir)
	t.Setenv("AIPERMILER_LOG_LEVEL", "bogus")
	s := startServe(t)
	waitForFile(t, filepath.Join(dir, "api.json"))
	s.cancel()
	<-s.exited
	if n := strings.Count(s.stderr.String(), "Invalid AIPERMILER_LOG_LEVEL"); n != 1 {
		t.Fatalf("the warning appeared %d times; want 1\n%s", n, s.stderr)
	}
}
