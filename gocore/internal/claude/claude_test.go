package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/claude"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// nativeStub is a file discovery accepts as a native claude binary (sparse, 2 MB).
func nativeStub(t *testing.T) string {
	t.Helper()
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(2 << 20); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// statusLineCommand returns the command passed through --settings.
func statusLineCommand(t *testing.T, args []string) string {
	t.Helper()
	var settings struct {
		StatusLine struct {
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	if len(args) < 2 || json.Unmarshal([]byte(args[1]), &settings) != nil {
		t.Fatalf("args = %q", args)
	}
	return settings.StatusLine.Command
}

// sinkPath reads the file argument of the status line command: single-quoted on
// POSIX, a bare word on Windows.
func sinkPath(t *testing.T, command string) string {
	t.Helper()
	_, word, ok := strings.Cut(command, " "+claude.SinkCommand+" ")
	if runtime.GOOS == "windows" {
		if !ok || strings.ContainsAny(word, " '\"") {
			t.Fatalf("command = %q", command)
		}
		return word
	}
	if !ok || len(word) < 2 || word[0] != '\'' || word[len(word)-1] != '\'' {
		t.Fatalf("command = %q", command)
	}
	return word[1 : len(word)-1]
}

func withStatus(t *testing.T, doc []byte, cwd, transcript string) []byte {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	v["cwd"], v["transcript_path"] = cwd, transcript
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFetch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  string // testdata sent on Esc, "" for none
		auth    string
		code    usage.Code
		reason  string
		windows int
		started bool
	}{
		{"usage", "statusline-2.1.316", `{"loggedIn":true}`, "", "", 2, true},
		{"no rate_limits", "statusline-2.1.316-start", `{"loggedIn":true}`, usage.CodeFormatDrift, "", 0, true},
		{"pro without rate_limits", "statusline-2.1.319-enterprise", `{"loggedIn":true,"subscriptionType":"pro"}`, usage.CodeFormatDrift, "", 0, true},
		{"enterprise on usage credits", "statusline-2.1.319-enterprise", `{"loggedIn":true,"subscriptionType":"enterprise"}`, usage.CodeUnavailable, claude.ReasonUsageCredits, 0, true},
		{"enterprise with quota windows", "statusline-2.1.316", `{"loggedIn":true,"subscriptionType":"enterprise"}`, "", "", 2, true},
		{"enterprise, status line never ran", "", `{"loggedIn":true,"subscriptionType":"enterprise"}`, usage.CodeFormatDrift, "", 0, true},
		{"status line never ran", "", `{"loggedIn":true}`, usage.CodeFormatDrift, "", 0, true},
		{"signed out", "", `{"loggedIn":false}`, usage.CodeSignedOut, "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base, projects := t.TempDir(), filepath.Join(t.TempDir(), "projects")
			folder := filepath.Join(projects, "-synthetic-probe")
			transcript := filepath.Join(folder, "00000000-0000-4000-8000-000000000000.jsonl")
			auth := strings.TrimSuffix(tt.auth, "}") + `,"projectsDirectory":` + strings.ReplaceAll(`"`+projects+`"`, `\`, `\\`) + "}"
			started := false
			start := func(_ string, args, _ []string, dir string) (claude.Terminal, error) {
				started = true
				command := statusLineCommand(t, args)
				self := "'/synthetic/core'"
				if runtime.GOOS == "windows" {
					self = "/synthetic/core"
				}
				if !strings.HasPrefix(command, self+" "+claude.SinkCommand+" ") {
					t.Errorf("command = %q", command)
				}
				sink := sinkPath(t, command)
				if err := os.MkdirAll(folder, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return newFake(screenPrompt, func(f *fake, key string) {
					if key == "\x1b" {
						runStatusLine(t, sink, withStatus(t, testdata(t, "statusline-2.1.316-start"), dir, transcript))
						if tt.status != "" {
							runStatusLine(t, sink, withStatus(t, testdata(t, tt.status), dir, transcript))
						}
					}
					panel := screenPanel
					if tt.reason == claude.ReasonUsageCredits {
						panel = screenCreditsPanel
					}
					showPanel(f, key, panel)
				}), nil
			}
			if tt.status == "" && tt.code == usage.CodeFormatDrift {
				// Not even the start run reaches the sink.
				start = func(_ string, args, _ []string, _ string) (claude.Terminal, error) {
					started = true
					statusLineCommand(t, args)
					return newFake(screenPrompt, signedIn), nil
				}
			}
			p := claude.ProviderForTest(start, func() []byte { return []byte(auth) }, base, "/synthetic/core")
			fs := flag.NewFlagSet("usage", flag.ContinueOnError)
			p.BindFlags(fs)
			if err := fs.Parse([]string{"--claude-bin", nativeStub(t)}); err != nil {
				t.Fatal(err)
			}
			got, err := p.Fetch(context.Background(), "")
			if tt.code != "" {
				assertCode(t, err, tt.code)
				var uerr *usage.Error
				if errors.As(err, &uerr) && uerr.Reason != tt.reason {
					t.Errorf("reason = %q, want %q", uerr.Reason, tt.reason)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(got.QuotaWindows) != tt.windows || started != tt.started {
				t.Fatalf("windows = %d, started = %v", len(got.QuotaWindows), started)
			}
			if exists(transcript) && tt.status != "" {
				t.Error("transcript kept")
			}
			if left, _ := filepath.Glob(filepath.Join(base, claude.SinkPatternForTest)); len(left) != 0 {
				t.Errorf("sink files left: %q", left)
			}
			if entries, _ := os.ReadDir(filepath.Join(base, "claude-probe")); len(entries) != 0 {
				t.Errorf("probe folder not empty: %v", entries)
			}
		})
	}
}

func TestFetchWithoutBinary(t *testing.T) {
	t.Parallel()
	p := claude.ProviderForTest(nil, nil, t.TempDir(), "/synthetic/core")
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	p.BindFlags(fs)
	if err := fs.Parse([]string{"--claude-bin", filepath.Join(t.TempDir(), "claude.js")}); err != nil {
		t.Fatal(err)
	}
	_, err := p.Fetch(context.Background(), "")
	assertCode(t, err, usage.CodeUnavailable)
}
