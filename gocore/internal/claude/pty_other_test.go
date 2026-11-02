//go:build !windows

package claude_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/claude"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// fakeClaudeEnv turns the test binary into a fake claude when the probe starts it:
// TestMain runs it before any flag parsing, so the claude flags never reach testing.
const fakeClaudeEnv = "AIPERMILER_FAKE_CLAUDE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeClaudeEnv); mode != "" {
		os.Exit(fakeClaude(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeClaude answers `auth status` and, in the PTY, draws a prompt, the usage panel
// after /usage and leaves on /exit (unless mode is "stuck"). The terminal is in
// cooked mode, so every Enter ends a line.
func fakeClaude(mode string, args []string) int {
	if len(args) > 0 && args[0] == "auth" {
		fmt.Printf(`{"loggedIn":%v,"authMethod":"claude.ai"}`, mode != "signed-out")
		return 0
	}
	if len(args) < 1 || args[0] != "--settings" {
		return 2
	}
	fmt.Print(screenPrompt)
	lines := bufio.NewScanner(os.Stdin)
	for lines.Scan() {
		switch line := lines.Text(); {
		case strings.Contains(line, "/usage"):
			fmt.Print(screenPanel)
		case strings.Contains(line, "/exit") && mode != "stuck":
			return 0
		}
	}
	return 0
}

func TestRealPTY(t *testing.T) {
	t.Parallel()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"ok", "stuck", "signed-out"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			start := time.Now()
			err := claude.RealProbeForTest(context.Background(), bin, t.TempDir(), []string{fakeClaudeEnv + "=" + mode})
			switch mode {
			case "signed-out":
				assertCode(t, err, usage.CodeSignedOut)
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			// "stuck" ignores /exit: it is killed after the exit grace, not left behind.
			if time.Since(start) > 5*time.Second {
				t.Fatalf("probe took %v", time.Since(start))
			}
		})
	}
}
