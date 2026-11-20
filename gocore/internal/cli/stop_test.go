package cli_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/cli"
	"github.com/suppressio/aipermiler/gocore/internal/provider"
	"github.com/suppressio/aipermiler/gocore/internal/service"
)

func runStop(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := cli.Env{Providers: provider.Registry{}, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, Context: t.Context()}
	code = cli.RunWith(env, append([]string{"service", "stop"}, args...))
	return code, out.String(), errOut.String()
}

func TestStopExitCodesAreDistinct(t *testing.T) {
	t.Parallel()
	codes := map[int]string{cli.ExitOK: "ok", cli.ExitProviderError: "error", cli.ExitUsage: "usage", cli.ExitAlreadyRunning: "already running", cli.ExitNotRunning: "not running", cli.ExitStopTimeout: "timeout"}
	if len(codes) != 6 {
		t.Fatalf("exit codes collide: %v", codes)
	}
}

func TestServiceStopNotRunning(t *testing.T) {
	t.Setenv("AIPERMILER_CONFIG_DIR", t.TempDir())
	code, out, _ := runStop(t, "--json")
	if code != cli.ExitNotRunning || out != `{"schemaVersion":1,"status":"not_running"}`+"\n" {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
	code, out, _ = runStop(t)
	if code != cli.ExitNotRunning || !strings.Contains(out, "not running") {
		t.Fatalf("text: exit = %d, out = %q", code, out)
	}
}

func TestServiceStopStopsServe(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIPERMILER_CONFIG_DIR", dir)
	s := startServe(t)
	waitForFile(t, dir+"/api.json")
	code, out, stderr := runStop(t, "--json")
	if code != cli.ExitOK || out != `{"schemaVersion":1,"status":"stopped"}`+"\n" {
		t.Fatalf("exit = %d, out = %q, stderr = %q", code, out, stderr)
	}
	select {
	case <-s.exited:
		if s.code != cli.ExitOK {
			t.Errorf("serve exit = %d; want 0", s.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve is still running")
	}
}

func TestServiceStopTextOnSuccess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIPERMILER_CONFIG_DIR", dir)
	startServe(t)
	waitForFile(t, dir+"/api.json")
	code, out, _ := runStop(t)
	if code != cli.ExitOK || !strings.Contains(out, "stopped") {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
}

func TestServiceStopTimeout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIPERMILER_CONFIG_DIR", dir)
	lock, err := service.AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	code, out, _ := runStop(t, "--json", "--timeout", "200ms")
	if code != cli.ExitStopTimeout || out != `{"schemaVersion":1,"status":"timeout"}`+"\n" {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
}

func TestServiceStopUsageErrors(t *testing.T) {
	t.Setenv("AIPERMILER_CONFIG_DIR", t.TempDir())
	for _, args := range [][]string{{"extra"}, {"--timeout", "0"}, {"--timeout", "soon"}, {"--timeout", "-1s"}, {"--port", "1"}} {
		if code, _, _ := runStop(t, args...); code != cli.ExitUsage {
			t.Errorf("service stop %v exit = %d; want %d", args, code, cli.ExitUsage)
		}
	}
	for _, args := range [][]string{{"service"}, {"service", "start"}} {
		var out, errOut bytes.Buffer
		env := cli.Env{Providers: provider.Registry{}, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, Context: t.Context()}
		if code := cli.RunWith(env, args); code != cli.ExitUsage {
			t.Errorf("%v exit = %d; want %d", args, code, cli.ExitUsage)
		}
	}
	var out bytes.Buffer
	env := cli.Env{Providers: provider.Registry{}, Stdin: strings.NewReader(""), Stdout: &out, Stderr: new(bytes.Buffer), Context: t.Context()}
	if code := cli.RunWith(env, []string{"service", "stop", "extra", "--json"}); code != cli.ExitUsage || !strings.Contains(out.String(), `"usage"`) {
		t.Errorf("json usage error: exit = %d, out = %q", code, out.String())
	}
}
