package refresh_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// The test binary doubles as a fake core CLI: FAKE_CLI picks what it does.
func TestMain(m *testing.M) {
	switch mode := os.Getenv("FAKE_CLI"); {
	case mode == "":
		os.Exit(m.Run())
	case strings.HasPrefix(mode, "print:"):
		// Writes the file named after "print:" and records its arguments.
		data, err := os.ReadFile(strings.TrimPrefix(mode, "print:"))
		if err != nil {
			os.Exit(9)
		}
		if file := os.Getenv("FAKE_ARGS"); file != "" {
			_ = os.WriteFile(file, []byte(strings.Join(os.Args[1:], "\n")), 0o600)
		}
		_, _ = os.Stdout.Write(data)
		os.Exit(0)
	case mode == "garbage":
		fmt.Println("claude: command not found")
		os.Exit(4)
	case mode == "flood":
		// Writes without end: only the output limit stops it before the timeout.
		chunk := strings.Repeat("x", 64<<10)
		for {
			if _, err := fmt.Print(chunk); err != nil {
				os.Exit(1)
			}
		}
	case mode == "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	case strings.HasPrefix(mode, "tree:"):
		// Starts a grandchild, writes both pids, then hangs.
		grandchild := exec.Command(os.Args[0]) //nolint:gosec // the test binary itself
		grandchild.Env = append(os.Environ(), "FAKE_CLI=sleep")
		if err := grandchild.Start(); err != nil {
			os.Exit(9)
		}
		pids := fmt.Sprintf("%d %d", os.Getpid(), grandchild.Process.Pid)
		_ = os.WriteFile(strings.TrimPrefix(mode, "tree:"), []byte(pids), 0o600)
		time.Sleep(time.Minute)
		os.Exit(0)
	default:
		os.Exit(9)
	}
}

func fixturePath(name string) string {
	return filepath.Join("..", "..", "contract", "v1", name+".json")
}

func fetcher() refresh.CLIFetcher {
	return refresh.CLIFetcher{Binary: os.Args[0], Timeout: 2 * time.Second, Margin: time.Second}
}

func ptr[T any](v T) *T { return &v }

func TestFetchParsesTheDocumentAndPassesTheAccountFlags(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	fixture, _ := filepath.Abs(fixturePath("usage-claude"))
	t.Setenv("FAKE_CLI", "print:"+fixture)
	t.Setenv("FAKE_ARGS", argsFile)
	u, err := fetcher().Fetch(context.Background(), store.Account{ID: "c1", Provider: "claude", BinaryPath: ptr("/opt/claude")})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.QuotaWindows) != 2 || u.QuotaWindows[0].ID != "claude-5h" {
		t.Errorf("usage = %+v", u)
	}
	args, _ := os.ReadFile(argsFile)
	want := "usage\nclaude\n--json\n--timeout\n2s\n--claude-bin\n/opt/claude"
	if string(args) != want {
		t.Errorf("args =\n%s\nwant\n%s", args, want)
	}
}

func TestUsageArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		acc  store.Account
		want string
	}{
		{store.Account{ID: "c", Provider: "claude"}, "usage claude --json --timeout 2m0s"},
		{store.Account{ID: "x", Provider: "codex", BinaryPath: ptr("/bin/codex")}, "usage codex --json --timeout 2m0s --codex-bin /bin/codex"},
		{store.Account{ID: "g", Provider: "copilot", Host: "acme.ghe.com", Scope: "organization"}, "usage copilot --json --timeout 2m0s --account g --host acme.ghe.com --scope organization"},
		{store.Account{ID: "g", Provider: "copilot", Host: "github.com", Scope: "personal", ManualQuota: 300}, "usage copilot --json --timeout 2m0s --account g --host github.com --scope personal --manual-quota 300"},
		{store.Account{ID: "g", Provider: "copilot"}, "usage copilot --json --timeout 2m0s --account g --manual-quota 0"},
	}
	for _, c := range cases {
		args, err := refresh.UsageArgs(c.acc, 2*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(args, " "); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.acc.Provider, got, c.want)
		}
	}
	if _, err := refresh.UsageArgs(store.Account{Provider: "gemini"}, time.Minute); err == nil {
		t.Error("unknown provider accepted")
	}
}

func TestFetchErrors(t *testing.T) {
	timeoutDoc, _ := filepath.Abs(fixturePath("error-timeout"))
	staleDoc, _ := filepath.Abs(fixturePath("usage-stale"))
	cases := []struct {
		name, mode string
		check      func(error) bool
	}{
		{"error document", "print:" + timeoutDoc, func(err error) bool {
			var e *usage.Error
			return errors.As(err, &e) && e.Code == usage.CodeTimeout
		}},
		{"stale values", "print:" + staleDoc, func(err error) bool {
			var e *usage.Error
			return errors.As(err, &e) && e.Code == usage.CodeUnavailable
		}},
		{"bad output", "garbage", func(err error) bool { return errors.Is(err, usage.ErrBadDocument) }},
		{"output over the limit", "flood", func(err error) bool { return errors.Is(err, usage.ErrBadDocument) }},
		{"hung child", "sleep", func(err error) bool { return errors.Is(err, refresh.ErrChildTimeout) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("FAKE_CLI", c.mode)
			f := fetcher()
			f.Timeout, f.Margin = 500*time.Millisecond, 2*time.Second
			_, err := f.Fetch(context.Background(), store.Account{ID: "c", Provider: "claude"})
			if err == nil || !c.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

// A stale document's reason reaches the error, so the service can serve it.
func TestFetchKeepsTheStaleReason(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "stale.json")
	data, err := os.ReadFile(fixturePath("usage-stale"))
	if err != nil {
		t.Fatal(err)
	}
	withReason := strings.Replace(string(data), `"message":"Provider is temporarily unavailable"}`, `"message":"Provider is temporarily unavailable","reason":"maintenance"}`, 1)
	if withReason == string(data) {
		t.Fatal("usage-stale.json changed: the test could not add a reason to it")
	}
	data = []byte(withReason)
	if err := os.WriteFile(doc, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLI", "print:"+doc)
	_, err = fetcher().Fetch(context.Background(), store.Account{ID: "c", Provider: "claude"})
	var e *usage.Error
	if !errors.As(err, &e) || e.Reason != "maintenance" || e.Code != usage.CodeUnavailable {
		t.Errorf("err = %#v", err)
	}
}

func TestFetchMissingBinary(t *testing.T) {
	t.Parallel()
	f := refresh.CLIFetcher{Binary: filepath.Join(t.TempDir(), "nope"), Timeout: time.Second}
	if _, err := f.Fetch(context.Background(), store.Account{ID: "c", Provider: "claude"}); err == nil {
		t.Error("missing binary gave no error")
	}
}

// A stop cancels the read and kills the child's whole tree.
func TestCancelKillsTheChildTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process liveness check below is Unix-only; the job object is checked on Windows by hand")
	}
	pidFile := filepath.Join(t.TempDir(), "pids")
	t.Setenv("FAKE_CLI", "tree:"+pidFile)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	f := fetcher()
	f.Timeout = time.Minute
	go func() {
		_, err := f.Fetch(ctx, store.Account{ID: "c", Provider: "claude"})
		done <- err
	}()
	var pids []int
	deadline := time.Now().Add(10 * time.Second)
	for len(pids) != 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		data, _ := os.ReadFile(pidFile)
		pids = pids[:0]
		for _, f := range strings.Fields(string(data)) {
			if n, err := strconv.Atoi(f); err == nil {
				pids = append(pids, n)
			}
		}
	}
	if len(pids) != 2 {
		t.Fatal("fake child did not start its grandchild")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v; want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Fetch did not return after the cancel")
	}
	for _, pid := range pids {
		gone := false
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if !alive(pid) {
				gone = true
				break
			}
		}
		if !gone {
			t.Errorf("process %d still alive", pid)
		}
	}
}

// raw runs `raw <provider> --json` and hands its document back byte for byte,
// with the error code of an error document (#248).
func TestRawPassesTheDocumentThrough(t *testing.T) {
	cases := []struct {
		fixture string
		code    usage.Code
	}{
		{"raw", ""},
		{"error-signed-out", usage.CodeSignedOut},
		{"error-usage", usage.CodeUsage},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			argsFile := filepath.Join(t.TempDir(), "args")
			path, _ := filepath.Abs(fixturePath(c.fixture))
			t.Setenv("FAKE_CLI", "print:"+path)
			t.Setenv("FAKE_ARGS", argsFile)
			f := fetcher()
			f.Timeout, f.RawTimeout = 7*time.Second, 2*time.Second // raw must not take the usage timeout
			doc, code, err := f.Raw(context.Background(), store.Account{ID: "g", Provider: "copilot", Host: "acme.ghe.com", Scope: "organization"})
			if err != nil {
				t.Fatal(err)
			}
			want, _ := os.ReadFile(path)
			if string(doc) != string(want) || code != c.code {
				t.Errorf("doc = %s code = %q; want %s %q", doc, code, want, c.code)
			}
			args, _ := os.ReadFile(argsFile)
			if got := strings.ReplaceAll(string(args), "\n", " "); got != "raw copilot --json --timeout 2s --account g --host acme.ghe.com --scope organization" {
				t.Errorf("args = %s", got)
			}
		})
	}
}

func TestRawArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		acc  store.Account
		want string
	}{
		{store.Account{ID: "c", Provider: "claude"}, "raw claude --json --timeout 25s"},
		{store.Account{ID: "g", Provider: "copilot"}, "raw copilot --json --timeout 25s --account g"},
	}
	for _, c := range cases {
		args, err := refresh.RawArgs(c.acc, 25*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(args, " "); got != c.want {
			t.Errorf("got %s; want %s", got, c.want)
		}
	}
	if _, err := refresh.RawArgs(store.Account{Provider: "gemini"}, time.Second); err == nil {
		t.Error("unknown provider accepted")
	}
}

func TestRawErrors(t *testing.T) {
	usageDoc, _ := filepath.Abs(fixturePath("usage-ok"))
	cases := []struct {
		name, mode string
		want       error
	}{
		{"not a document", "garbage", usage.ErrBadDocument},
		{"a document of another kind", "print:" + usageDoc, usage.ErrBadDocument},
		{"hung child", "sleep", refresh.ErrChildTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("FAKE_CLI", c.mode)
			f := fetcher()
			f.RawTimeout, f.Margin = 500*time.Millisecond, 2*time.Second
			if _, _, err := f.Raw(context.Background(), store.Account{ID: "g", Provider: "copilot"}); !errors.Is(err, c.want) {
				t.Errorf("err = %v; want %v", err, c.want)
			}
		})
	}
}
