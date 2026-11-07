package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
	"github.com/suppressio/aipermiler/gocore/internal/cli"
	"github.com/suppressio/aipermiler/gocore/internal/provider"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

type fakeProvider struct {
	account       string
	acceptsSecret bool
	fetch         func(context.Context, string, string) (usage.Usage, error)
}

func (*fakeProvider) ID() string            { return "fake" }
func (*fakeProvider) DisplayName() string   { return "Fake" }
func (p *fakeProvider) AcceptsSecret() bool { return p.acceptsSecret }
func (p *fakeProvider) BindFlags(fs *flag.FlagSet) {
	fs.StringVar(&p.account, "account", "", "Account identifier")
}
func (p *fakeProvider) Fetch(ctx context.Context, secret string) (usage.Usage, error) {
	if p.fetch != nil {
		return p.fetch(ctx, secret, p.account)
	}
	return sampleUsage(), nil
}
func ptr[T any](v T) *T   { return &v }
func fixedNow() time.Time { return time.Date(2026, time.October, 31, 21, 10, 0, 123, time.UTC) }
func sampleUsage() usage.Usage {
	return usage.Usage{PlanTier: ptr("plus"), QuotaWindows: []usage.QuotaWindow{
		{ID: "five-hour", Label: "5 hours", PeriodType: usage.PeriodRollingHours, PeriodLength: ptr(5.0), Unit: usage.UnitPercentage, Used: 21, Total: ptr(100.0), ResetsAt: ptr(fixedNow().Add(time.Hour))},
		{ID: "weekly", Label: "7 days", PeriodType: usage.PeriodRollingDays, PeriodLength: ptr(7.0), Unit: usage.UnitPercentage, Used: 42, Total: ptr(100.0), ResetsAt: ptr(fixedNow().Add(7 * 24 * time.Hour))},
	}}
}
func run(t *testing.T, args []string, stdin io.Reader, factory func() provider.Provider) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := cli.Env{Providers: provider.Registry{"fake": factory}, Stdin: stdin, Stdout: &stdout, Stderr: &stderr, Now: fixedNow, Location: time.UTC}
	code := cli.RunWith(env, args)
	return code, stdout.String(), stderr.String()
}
func standardFake() provider.Provider { return &fakeProvider{acceptsSecret: true} }
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contract", "v1", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestContractFixtures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value usage.Usage
		err   error
		args  []string
		exit  int
	}{
		{name: "usage-ok", value: sampleUsage()},
		{name: "usage-count-window", value: usage.Usage{SubscriptionRenewsAt: ptr(fixedNow().Add(30 * 24 * time.Hour)), QuotaWindows: []usage.QuotaWindow{{ID: "credits", Label: "Monthly credits", PeriodType: usage.PeriodBillingCycle, PeriodLength: ptr(1.0), Unit: usage.UnitCount, Used: 250, Total: ptr(1000.0), ResetsAt: ptr(fixedNow().Add(30 * 24 * time.Hour))}}}},
		{name: "usage-stale", value: func() usage.Usage {
			u := sampleUsage()
			u.Stale = &usage.StaleInfo{Code: usage.CodeUnavailable, Message: "Provider is temporarily unavailable"}
			return u
		}()},
		{name: "error-signed-out", err: &usage.Error{Code: usage.CodeSignedOut, Message: "Sign in required"}, exit: cli.ExitProviderError},
		{name: "error-not-ready", err: &usage.Error{Code: usage.CodeNotReady, Message: "Usage is not ready yet"}, exit: cli.ExitProviderError},
		{name: "error-timeout", err: fmt.Errorf("fetch: %w", context.DeadlineExceeded), exit: cli.ExitProviderError},
		{name: "error-format-drift", err: &usage.Error{Code: usage.CodeFormatDrift, Message: "Unrecognized usage response", Endpoint: "/usage", Shape: usage.ExtractShape(map[string]any{"active": true, "items": []any{map[string]any{"remaining": 42}}, "label": "Private", "reset": nil})}, exit: cli.ExitProviderError},
		{name: "error-unavailable", err: errors.New("Provider is temporarily unavailable"), exit: cli.ExitProviderError},
		{name: "error-usage", args: []string{"usage", "nope", "--json"}, exit: cli.ExitUsage},
		{name: "version", args: []string{"version", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := tt.args
			if args == nil {
				args = []string{"usage", "fake", "--json"}
			}
			code, out, _ := run(t, args, strings.NewReader(""), func() provider.Provider {
				return &fakeProvider{fetch: func(context.Context, string, string) (usage.Usage, error) { return tt.value, tt.err }}
			})
			want := fixture(t, tt.name)
			if tt.name == "version" {
				var v struct {
					SchemaVersion int    `json:"schemaVersion"`
					Name          string `json:"name"`
					Version       string `json:"version"`
					OS            string `json:"os"`
					Arch          string `json:"arch"`
				}
				if err := json.Unmarshal(want, &v); err != nil {
					t.Fatal(err)
				}
				v.Name, v.Version, v.OS, v.Arch = buildinfo.Name, buildinfo.Version, runtime.GOOS, runtime.GOARCH
				data, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				data = append(data, '\n')
				want = data
			}
			if code != tt.exit || out != string(want) {
				t.Errorf("exit = %d; want %d\noutput = %s\nwant = %s", code, tt.exit, out, want)
			}
		})
	}
}

func TestAllFixturesAreVersionedDocuments(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("..", "..", "contract", "v1", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 16 {
		t.Fatalf("fixture count = %d; want 16", len(files))
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()
			data, readErr := os.ReadFile(file)
			if readErr != nil {
				t.Fatal(readErr)
			}
			var doc struct {
				SchemaVersion int `json:"schemaVersion"`
			}
			if decodeErr := json.Unmarshal(data, &doc); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if doc.SchemaVersion != usage.SchemaVersion {
				t.Errorf("schemaVersion = %d", doc.SchemaVersion)
			}
			compact := new(bytes.Buffer)
			if compactErr := json.Compact(compact, data); compactErr != nil {
				t.Fatal(compactErr)
			}
			if !bytes.Equal(data, append(compact.Bytes(), '\n')) {
				t.Error("fixture must be compact JSON with one trailing newline")
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		stdin   io.Reader
		factory func() provider.Provider
	}{
		{name: "unknown provider", args: []string{"usage", "nope"}},
		{name: "missing provider", args: []string{"usage"}},
		{name: "bad flag", args: []string{"usage", "fake", "--unknown"}},
		{name: "secret flag forbidden", args: []string{"usage", "fake", "--secret=forbidden"}},
		{name: "extra args", args: []string{"usage", "fake", "extra"}},
		{name: "zero timeout", args: []string{"usage", "fake", "--timeout", "0"}},
		{name: "negative timeout", args: []string{"usage", "fake", "--timeout=-1s"}},
		{name: "long timeout", args: []string{"usage", "fake", "--timeout", "121s"}},
		{name: "invalid timeout", args: []string{"usage", "fake", "--timeout", "soon"}},
		{name: "empty secret", args: []string{"usage", "fake", "--secret-stdin"}, stdin: strings.NewReader("\r\n")},
		{name: "large secret", args: []string{"usage", "fake", "--secret-stdin"}, stdin: strings.NewReader(strings.Repeat("s", 64*1024+1))},
		{name: "read error", args: []string{"usage", "fake", "--secret-stdin"}, stdin: brokenReader{}},
		{name: "secret refused", args: []string{"usage", "fake", "--secret-stdin"}, factory: func() provider.Provider { return &fakeProvider{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, jsonMode := range []bool{false, true} {
				args := append([]string{}, tt.args...)
				if jsonMode {
					args = append(args, "--json")
				}
				factory := tt.factory
				if factory == nil {
					factory = standardFake
				}
				stdin := tt.stdin
				if stdin == nil {
					stdin = strings.NewReader("")
				}
				code, out, stderr := run(t, args, stdin, factory)
				if code != cli.ExitUsage {
					t.Errorf("exit = %d; want 2", code)
				}
				if jsonMode {
					var doc struct {
						SchemaVersion int             `json:"schemaVersion"`
						Provider      *string         `json:"provider"`
						Error         usage.StaleInfo `json:"error"`
					}
					if err := json.Unmarshal([]byte(out), &doc); err != nil {
						t.Fatal(err)
					}
					if doc.SchemaVersion != 1 || doc.Error.Code != usage.CodeUsage || doc.Error.Message == "" || doc.Provider != nil {
						t.Errorf("invalid usage error: %s", out)
					}
					if strings.Contains(stderr, "Usage:") {
						t.Errorf("usage text leaked to stderr: %s", stderr)
					}
				} else if out != "" || !strings.Contains(stderr, "Usage:") {
					t.Errorf("unexpected streams: stdout=%q stderr=%q", out, stderr)
				}
			}
		})
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("Read failed") }

func TestFlagsAndSecret(t *testing.T) {
	t.Parallel()
	for _, ending := range []string{"", "\n", "\r\n"} {
		t.Run(fmt.Sprintf("ending-%q", ending), func(t *testing.T) {
			t.Parallel()
			const secret = "synthetic-private-token"
			factory := func() provider.Provider {
				return &fakeProvider{acceptsSecret: true, fetch: func(ctx context.Context, gotSecret, account string) (usage.Usage, error) {
					if gotSecret != secret || account != "work" {
						t.Errorf("Fetch received secret=%q account=%q", gotSecret, account)
					}
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 2*time.Minute {
						t.Error("invalid fetch deadline")
					}
					return sampleUsage(), nil
				}}
			}
			code, out, stderr := run(t, []string{"usage", "fake", "--json", "--account", "work", "--timeout", "2m", "--secret-stdin"}, strings.NewReader(secret+ending), factory)
			if code != cli.ExitOK || strings.Contains(out+stderr, secret) {
				t.Errorf("exit = %d or leaked secret", code)
			}
		})
	}
}

func TestSecretErrorsAreRedacted(t *testing.T) {
	t.Parallel()
	for _, jsonMode := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonMode), func(t *testing.T) {
			t.Parallel()
			args := []string{"usage", "fake", "--secret-stdin"}
			if jsonMode {
				args = append(args, "--json")
			}
			code, out, stderr := run(t, args, strings.NewReader("synthetic-secret"), func() provider.Provider {
				return &fakeProvider{acceptsSecret: true, fetch: func(_ context.Context, secret, _ string) (usage.Usage, error) {
					return usage.Usage{}, fmt.Errorf("Cannot authenticate %s\nTry again", secret)
				}}
			})
			if code != 1 || strings.Contains(out+stderr, "synthetic-secret") {
				t.Error("secret leaked or wrong exit code")
			}
			if !jsonMode && strings.Count(stderr, "\n") != 1 {
				t.Errorf("text error must occupy one line: %q", stderr)
			}
		})
	}
}

func TestFetchTimeout(t *testing.T) {
	t.Parallel()
	code, out, _ := run(t, []string{"usage", "fake", "--json", "--timeout", "50ms"}, strings.NewReader(""), func() provider.Provider {
		return &fakeProvider{fetch: func(ctx context.Context, _, _ string) (usage.Usage, error) {
			<-ctx.Done()
			return usage.Usage{}, fmt.Errorf("fetch: %w", ctx.Err())
		}}
	})
	if code != 1 || out != string(fixture(t, "error-timeout")) {
		t.Errorf("exit=%d output=%s", code, out)
	}
}

func TestEmptyUsageAndUTC(t *testing.T) {
	t.Parallel()
	code, out, _ := run(t, []string{"usage", "fake", "--json"}, strings.NewReader(""), func() provider.Provider {
		return &fakeProvider{fetch: func(context.Context, string, string) (usage.Usage, error) { return usage.Usage{}, nil }}
	})
	want := `{"schemaVersion":1,"provider":"fake","fetchedAt":"2026-10-31T21:10:00Z","stale":false,"usage":{"planTier":null,"subscriptionRenewsAt":null,"quotaWindows":[]}}` + "\n"
	if code != 0 || out != want {
		t.Errorf("exit=%d output=%s", code, out)
	}
	code, out, _ = run(t, []string{"usage", "fake"}, strings.NewReader(""), standardFake)
	if code != 0 || out != "Fake · 5h 21% · resets 22:10\nFake · 7d 42% · resets 2026-11-07 21:10\n" {
		t.Errorf("exit=%d text=%q", code, out)
	}
}

func TestJSONDetectedAnywhere(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--json"}, {"unknown", "--json"}, {"--json", "usage", "fake"}, {"version", "extra", "--json"}, {"usage", "fake", "--bad", "--json"}, {"help", "--json"}, {"version", "--help", "--json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			code, out, _ := run(t, args, strings.NewReader(""), standardFake)
			if code != 2 || !json.Valid([]byte(out)) {
				t.Errorf("exit=%d output=%q", code, out)
			}
		})
	}
}

func TestFactoryPerRun(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"usage", "fake", "--account", "work"}, {"usage", "fake"}} {
		expected := ""
		if len(args) > 2 {
			expected = "work"
		}
		factory := func() provider.Provider {
			return &fakeProvider{fetch: func(_ context.Context, _, account string) (usage.Usage, error) {
				if account != expected {
					t.Errorf("account = %q; want %q", account, expected)
				}
				return usage.Usage{}, nil
			}}
		}
		if code, _, _ := run(t, args, strings.NewReader(""), factory); code != 0 {
			t.Errorf("exit=%d", code)
		}
	}
}

func TestSecretInputBoundaries(t *testing.T) {
	t.Parallel()
	for _, secret := range []string{strings.Repeat("s", 64*1024), " space ", "token\n", "token\r"} {
		t.Run(fmt.Sprintf("length-%d-last-%q", len(secret), secret[len(secret)-1:]), func(t *testing.T) {
			t.Parallel()
			// Add a newline only when testing whitespace; the 64 KiB limit includes it.
			input := secret
			if len(secret) < 64*1024 && !strings.HasSuffix(secret, "\r") {
				input += "\n"
			}
			code, _, _ := run(t, []string{"usage", "fake", "--secret-stdin"}, strings.NewReader(input), func() provider.Provider {
				return &fakeProvider{acceptsSecret: true, fetch: func(_ context.Context, got, _ string) (usage.Usage, error) {
					if got != secret {
						t.Errorf("secret was trimmed beyond one newline: length %d; want %d", len(got), len(secret))
					}
					return usage.Usage{}, nil
				}}
			})
			if code != 0 {
				t.Errorf("exit = %d", code)
			}
		})
	}
}

func TestDefaultSecretDoesNotReadStdin(t *testing.T) {
	t.Parallel()
	code, _, _ := run(t, []string{"usage", "fake"}, brokenReader{}, func() provider.Provider {
		return &fakeProvider{fetch: func(_ context.Context, secret, _ string) (usage.Usage, error) {
			if secret != "" {
				t.Error("unexpected secret")
			}
			return usage.Usage{}, nil
		}}
	})
	if code != 0 {
		t.Errorf("exit=%d", code)
	}
}

func TestStaleReasonRedactsSecret(t *testing.T) {
	t.Parallel()
	code, out, _ := run(t, []string{"usage", "fake", "--json", "--secret-stdin"}, strings.NewReader("synthetic-secret"), func() provider.Provider {
		return &fakeProvider{acceptsSecret: true, fetch: func(_ context.Context, secret, _ string) (usage.Usage, error) {
			u := sampleUsage()
			u.Stale = &usage.StaleInfo{Code: usage.CodeUnavailable, Message: "Failed: " + secret}
			return u, nil
		}}
	})
	if code != 0 || strings.Contains(out, "synthetic-secret") || !strings.Contains(out, `"stale":true`) {
		t.Errorf("unexpected stale output: %s", out)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("Output failed") }

func TestOutputWriteFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"text usage", []string{"usage", "fake"}},
		{"JSON usage", []string{"usage", "fake", "--json"}},
		{"text version", []string{"version"}},
		{"JSON version", []string{"version", "--json"}},
		{"usage error", []string{"usage", "nope", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			code := cli.RunWith(cli.Env{Providers: provider.Registry{"fake": standardFake}, Stdin: strings.NewReader(""), Stdout: brokenWriter{}, Stderr: &stderr, Now: fixedNow, Location: time.UTC}, tt.args)
			if code != cli.ExitProviderError || !strings.Contains(stderr.String(), "Cannot write output: Output failed") {
				t.Errorf("exit=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestJSONFlagValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args     []string
		wantCode int
		wantJSON bool
	}{
		{[]string{"version", "--json=false"}, cli.ExitOK, false},
		{[]string{"version", "--json=true"}, cli.ExitOK, true},
		{[]string{"version", "--json", "--json=false"}, cli.ExitOK, false},
		{[]string{"version", "--json=false", "--json"}, cli.ExitOK, true},
		{[]string{"help", "--json=false"}, cli.ExitOK, false},
		{[]string{"unknown", "--json=false"}, cli.ExitUsage, false},
		{[]string{"usage", "fake", "--bad", "--json=false"}, cli.ExitUsage, false},
		{[]string{"usage", "fake", "--json=0"}, cli.ExitOK, false},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			t.Parallel()
			code, out, errOut := run(t, tt.args, strings.NewReader(""), standardFake)
			if code != tt.wantCode {
				t.Fatalf("exit=%d; want %d (stdout=%q stderr=%q)", code, tt.wantCode, out, errOut)
			}
			isJSON := strings.HasPrefix(out, "{") && json.Valid([]byte(out))
			if isJSON != tt.wantJSON {
				t.Errorf("JSON output=%v; want %v (stdout=%q)", isJSON, tt.wantJSON, out)
			}
			if !tt.wantJSON && tt.wantCode == cli.ExitUsage && out != "" {
				t.Errorf("text usage error wrote to stdout: %q", out)
			}
		})
	}
}
