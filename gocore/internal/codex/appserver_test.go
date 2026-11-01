package codex_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
	"github.com/suppressio/aipermiler/gocore/internal/cli"
	"github.com/suppressio/aipermiler/gocore/internal/codex"
	"github.com/suppressio/aipermiler/gocore/internal/provider"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

func TestMain(m *testing.M) {
	if scenario := os.Getenv("AIPERMILER_FAKE_CODEX"); scenario != "" {
		os.Exit(fakeAppServer(scenario))
	}
	os.Exit(m.Run())
}

// The PID marker lets the parent verify process cleanup even after Kill.
func fakeAppServer(scenario string) int {
	if len(os.Args) != 2 || os.Args[1] != "app-server" {
		return 90
	}
	if err := os.WriteFile(os.Getenv("AIPERMILER_FAKE_MARKER"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return 92
	}
	if scenario == "exit" {
		return 7
	}
	if scenario == "hang-initialize" {
		time.Sleep(time.Minute)
		return 93
	}
	scanner := bufio.NewScanner(os.Stdin)
	methods := []string{"initialize", "initialized", "account/rateLimits/read"}
	if scenario == "signed-out" || scenario == "api-key" {
		methods = append(methods, "account/read")
	}
	for _, method := range methods {
		if !scanner.Scan() {
			return 94
		}
		var req map[string]json.RawMessage
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			return 95
		}
		var gotMethod string
		if json.Unmarshal(req["method"], &gotMethod) != nil || gotMethod != method || req["jsonrpc"] != nil {
			return 96
		}
		switch method {
		case "initialize":
			want := fmt.Sprintf(`{"clientInfo":{"name":"aipermiler","title":null,"version":%q}}`, buildinfo.Version)
			var gotParams, wantParams any
			if json.Unmarshal(req["params"], &gotParams) != nil || json.Unmarshal([]byte(want), &wantParams) != nil || !reflect.DeepEqual(gotParams, wantParams) || string(req["id"]) != "1" {
				return 97
			}
			fakePrint(`{"id":1,"result":{"userAgent":"synthetic"}}`)
		case "initialized":
			if len(req) != 1 {
				return 98
			}
		case "account/rateLimits/read":
			if string(req["params"]) != "null" || string(req["id"]) != "2" {
				return 99
			}
			switch scenario {
			case "signed-out", "api-key":
				fakePrint(`{"id":2,"error":{"code":-32600,"message":"authentication required"}}`)
			case "internal":
				fakePrint(`{"id":2,"error":{"code":-32603,"message":"synthetic offline"}}`)
			case "other-error":
				fakePrint(`{"id":2,"error":{"code":-12345,"message":"synthetic RPC failure"}}`)
			case "hang":
				time.Sleep(time.Minute)
				return 100
			case "scalar":
				fakePrint(`{"id":2,"result":42}`)
			case "missing-result":
				fakePrint(`{"id":2}`)
			default:
				fakePrint("garbage before the answer")
				fakePrint(`{"method":"account/rateLimits/updated","params":null}`)
				fakePrint(`{"id":999,"result":null}`)
				fakePrint(`{"method":"synthetic/notification","params":"` + strings.Repeat("x", 100*1024) + `"}`)
				if !fakeFixture(2, "plus") {
					return 101
				}
			}
		case "account/read":
			if string(req["params"]) != `{"refreshToken":false}` || string(req["id"]) != "3" {
				return 103
			}
			name := "account-signed-out"
			if scenario == "api-key" {
				name = "account-api-key"
			}
			if !fakeFixture(3, name) {
				return 104
			}
		}
	}
	// Any extra method violates the read-only protocol. EOF is clean shutdown.
	if scanner.Scan() {
		return 106
	}
	if scenario == "never-exits" {
		time.Sleep(time.Minute)
	}
	return 0
}

func fakePrint(s string) {
	_, _ = fmt.Fprintln(os.Stdout, s)
	_, _ = fmt.Fprintln(os.Stderr, "synthetic private stderr must never escape")
}
func fakeFixture(id int, name string) bool {
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		return false
	}
	compact := new(bytes.Buffer)
	if json.Compact(compact, data) != nil {
		return false
	}
	fakePrint(fmt.Sprintf(`{"id":%d,"result":%s}`, id, compact))
	return true
}

func fakeProvider(t *testing.T, timeout, grace time.Duration) *codex.Provider {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := codex.Discovery{Getenv: os.Getenv, Stat: os.Stat, EvalSymlinks: filepath.EvalSymlinks, ReadDir: os.ReadDir, UserHomeDir: os.UserHomeDir, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	p := codex.NewForTest(d, timeout, grace)
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	p.BindFlags(fs)
	if err = fs.Parse([]string{"--codex-bin", binary}); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertProcessGone(t *testing.T, marker string) {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal("fake app-server did not start: ", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if processAlive(pid) {
		t.Fatal("fake app-server still alive after Fetch")
	}
}

func TestProbe(t *testing.T) {
	t.Setenv("AIPERMILER_FAKE_CODEX", "ok")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	tests := []struct {
		scenario string
		code     usage.Code
		message  string
	}{
		{"ok", "", ""}, {"signed-out", usage.CodeSignedOut, "Sign in with Codex (CLI or extension)"},
		{"api-key", usage.CodeUnavailable, "Codex is signed in with an API key, which has no subscription quota"},
		{"internal", usage.CodeUnavailable, "codex app-server: synthetic offline"},
		{"other-error", usage.CodeUnavailable, "codex app-server: -12345: synthetic RPC failure"},
		{"exit", usage.CodeUnavailable, "exit code 7"},
		{"hang", usage.CodeTimeout, "timed out"}, {"hang-initialize", usage.CodeTimeout, "timed out"},
		{"never-exits", "", ""}, {"scalar", usage.CodeFormatDrift, "no quota window recognized"}, {"missing-result", usage.CodeFormatDrift, "no quota window recognized"},
	}
	for _, tt := range tests {
		t.Run(tt.scenario, func(t *testing.T) {
			t.Setenv("AIPERMILER_FAKE_CODEX", tt.scenario)
			marker := filepath.Join(t.TempDir(), "address")
			t.Setenv("AIPERMILER_FAKE_MARKER", marker)
			p := fakeProvider(t, time.Second, 100*time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if tt.scenario == "hang" {
				ctx, cancel = context.WithTimeout(ctx, 250*time.Millisecond)
				defer cancel()
			}
			u, err := p.Fetch(ctx, "")
			assertProcessGone(t, marker)
			if tt.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				want, mapErr := codex.MapForTest(fixture(t, "plus"))
				if mapErr != nil {
					t.Fatal(mapErr)
				}
				if !reflect.DeepEqual(u, want) {
					t.Errorf("usage = %#v; want %#v", u, want)
				}
				return
			}
			var e *usage.Error
			if !errors.As(err, &e) || e.Code != tt.code || !strings.Contains(e.Message, tt.message) {
				t.Fatalf("error = %#v; want %s %q", err, tt.code, tt.message)
			}
			if strings.Contains(e.Message, "private stderr") {
				t.Fatal("stderr leaked")
			}
		})
	}
}

func TestCLI(t *testing.T) {
	t.Setenv("AIPERMILER_FAKE_CODEX", "ok")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, jsonMode := range []bool{true, false} {
		t.Run(fmt.Sprint(jsonMode), func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "address")
			t.Setenv("AIPERMILER_FAKE_MARKER", marker)
			var out, stderr bytes.Buffer
			env := cli.Env{Providers: provider.Default(), Stdin: strings.NewReader(""), Stdout: &out, Stderr: &stderr, Now: func() time.Time { return time.Date(2026, 10, 31, 21, 10, 0, 0, time.UTC) }, Location: time.UTC}
			args := []string{"usage", "codex", "--codex-bin", binary}
			if jsonMode {
				args = append(args, "--json")
			}
			code := cli.RunWith(env, args)
			assertProcessGone(t, marker)
			want := "Codex · 5h 21% · resets 22:10\nCodex · 7d 42% · resets 2026-11-07 21:10\n"
			if jsonMode {
				want = `{"schemaVersion":1,"provider":"codex","fetchedAt":"2026-10-31T21:10:00Z","stale":false,"usage":{"planTier":"plus","subscriptionRenewsAt":null,"quotaWindows":[{"id":"codex-5h","label":"5-hour limit","periodType":"rolling-hours","periodLength":5,"unit":"percentage","used":21,"total":100,"resetsAt":"2026-10-31T22:10:00Z"},{"id":"codex-7d","label":"7-day limit","periodType":"rolling-days","periodLength":7,"unit":"percentage","used":42,"total":100,"resetsAt":"2026-11-07T21:10:00Z"}]}}` + "\n"
			}
			if code != 0 || out.String() != want || stderr.Len() != 0 {
				t.Errorf("exit=%d output=%s stderr=%s; want=%s", code, &out, &stderr, want)
			}
		})
	}
}

func TestProviderFlags(t *testing.T) {
	t.Parallel()
	p := codex.New()
	if p.ID() != "codex" || p.DisplayName() != "Codex" || p.AcceptsSecret() {
		t.Fatal("wrong provider metadata")
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	p.BindFlags(fs)
	if err := fs.Parse([]string{"--codex-bin", "relative-path"}); err != nil {
		t.Fatal(err)
	}
	_, err := p.Fetch(context.Background(), "")
	var e *usage.Error
	if !errors.As(err, &e) || e.Code != usage.CodeUnavailable || e.Message != "codex binary path must be absolute" {
		t.Fatalf("error = %#v", err)
	}
	var out, stderr bytes.Buffer
	code := cli.RunWith(cli.Env{Providers: provider.Default(), Stdin: strings.NewReader("secret"), Stdout: &out, Stderr: &stderr}, []string{"usage", "codex", "--secret-stdin", "--json"})
	if code != cli.ExitUsage || !strings.Contains(out.String(), "Provider does not accept a secret on stdin") {
		t.Fatalf("exit=%d output=%s", code, &out)
	}
}
