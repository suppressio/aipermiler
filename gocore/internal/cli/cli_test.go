package cli_test

import (
	"bytes"
	"fmt"
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
