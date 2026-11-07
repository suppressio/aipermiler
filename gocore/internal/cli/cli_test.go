package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
	"github.com/suppressio/aipermiler/gocore/internal/cli"
)

func TestRun(t *testing.T) { // uses t.Setenv; paralleltest recognizes this constraint
	t.Setenv("AIPERMILER_LOG_LEVEL", "warn")
	wantVersion := fmt.Sprintf("%s %s (%s/%s)\n", buildinfo.Name, buildinfo.Version, runtime.GOOS, runtime.GOARCH)
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantOutput string
		wantUsage  bool
	}{
		{"version", []string{"version"}, cli.ExitOK, wantVersion, false},
		{"help", []string{"help"}, cli.ExitOK, "", true},
		{"short help", []string{"-h"}, cli.ExitOK, "", true},
		{"long help", []string{"--help"}, cli.ExitOK, "", true},
		{"version help", []string{"version", "--help"}, cli.ExitOK, "", true},
		{"no arguments", nil, cli.ExitUsage, "", true},
		{"unknown command", []string{"unknown"}, cli.ExitUsage, "", true},
		{"missing provider", []string{"usage"}, cli.ExitUsage, "", true},
		{"unknown provider", []string{"usage", "nope"}, cli.ExitUsage, "", true},
		{"serve is not implemented", []string{"serve"}, cli.ExitUsage, "", true},
		{"unknown flag", []string{"version", "--unknown"}, cli.ExitUsage, "", true},
		{"unexpected argument", []string{"version", "extra"}, cli.ExitUsage, "", true},
		{"unexpected help argument", []string{"help", "extra"}, cli.ExitUsage, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			code := cli.Run(tt.args, strings.NewReader(""), &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("Run() = %d; want %d", code, tt.wantCode)
			}
			output, unused := stdout.String(), stderr.String()
			if tt.wantCode == cli.ExitUsage {
				output, unused = stderr.String(), stdout.String()
			}
			if unused != "" {
				t.Errorf("unexpected output on unused stream: %q", unused)
			}
			if tt.wantUsage {
				for _, text := range []string{"Usage: " + buildinfo.Name, "version", "help"} {
					if !strings.Contains(output, text) {
						t.Errorf("output %q does not contain %q", output, text)
					}
				}
			} else if output != tt.wantOutput {
				t.Errorf("output = %q; want %q", output, tt.wantOutput)
			}
		})
	}
}

func TestRunLogsToStderr(t *testing.T) { // uses t.Setenv; paralleltest recognizes this constraint
	t.Setenv("AIPERMILER_LOG_LEVEL", "debug")
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"version"}, strings.NewReader(""), &stdout, &stderr); code != cli.ExitOK {
		t.Fatalf("Run() = %d; want %d", code, cli.ExitOK)
	}
	if !strings.HasPrefix(stdout.String(), buildinfo.Name+" "+buildinfo.Version+" (") || strings.Contains(stdout.String(), "level=") {
		t.Errorf("stdout contains unexpected command output: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "level=DEBUG") || !strings.Contains(stderr.String(), "version="+buildinfo.Version) {
		t.Errorf("stderr is missing the startup debug log: %q", stderr.String())
	}
}

// #180: `paths` names the Claude probe folder, for clients that read Claude Code
// sessions and must skip the probes' own.
func TestPaths(t *testing.T) { // uses t.Setenv
	t.Setenv("AIPERMILER_LOG_LEVEL", "warn")
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // Linux; other systems keep their own folder
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache folder:", err)
	}
	want := filepath.Join(base, buildinfo.Name, "claude-probe")

	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"paths", "--json"}, strings.NewReader(""), &stdout, &stderr); code != cli.ExitOK {
		t.Fatalf("code = %d; stderr %q", code, stderr.String())
	}
	var doc struct {
		SchemaVersion int    `json:"schemaVersion"`
		ClaudeProbe   string `json:"claudeProbe"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || doc.SchemaVersion != 1 || doc.ClaudeProbe != want {
		t.Fatalf("document %q (%v); want claudeProbe %q", stdout.String(), err, want)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("paths created the probe folder (stat err %v)", err)
	}

	stdout.Reset()
	if code := cli.Run([]string{"paths"}, strings.NewReader(""), &stdout, &stderr); code != cli.ExitOK || stdout.String() != "claude probe: "+want+"\n" {
		t.Fatalf("text: code %d, %q", code, stdout.String())
	}
	if code := cli.Run([]string{"paths", "extra"}, strings.NewReader(""), &stdout, &stderr); code != cli.ExitUsage {
		t.Fatalf("extra argument: code %d", code)
	}
}

// #180: without a user cache folder `paths` fails as unavailable, not with a usage error.
func TestPathsWithoutCacheFolder(t *testing.T) { // uses t.Setenv
	t.Setenv("AIPERMILER_LOG_LEVEL", "warn")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("LocalAppData", "")
	if _, err := os.UserCacheDir(); err == nil {
		t.Skip("this system finds a cache folder without HOME")
	}
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"paths", "--json"}, strings.NewReader(""), &stdout, &stderr); code != cli.ExitProviderError || !strings.Contains(stdout.String(), `"code":"unavailable"`) {
		t.Fatalf("code %d, %q", code, stdout.String())
	}
}
