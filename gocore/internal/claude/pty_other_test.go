//go:build !windows

package claude_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/claude"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// fakeClaudeEnv turns the test binary into a fake claude when the probe starts it:
// TestMain runs it before any flag parsing, so the claude flags never reach testing.
const (
	fakeClaudeEnv = "AIPERMILER_FAKE_CLAUDE"
	fakePIDEnv    = "AIPERMILER_FAKE_CLAUDE_PID"
)

// descendant starts a sleeping copy of the test binary, like a claude helper.
func descendant(stdout *os.File) *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), fakeClaudeEnv+"=sleep")
	cmd.Stdout = stdout
	if cmd.Start() != nil {
		os.Exit(3)
	}
	return cmd
}

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
	if mode == "sleep" {
		// Ignore the SIGHUP the kernel sends when the session leader dies: only the
		// process group kill may end this helper.
		signal.Ignore(syscall.SIGHUP)
		time.Sleep(10 * time.Second)
		return 0
	}
	if len(args) > 0 && args[0] == "auth" {
		if mode == "auth-leak" {
			// A helper keeps stdout open after auth status itself exits.
			descendant(os.Stdout)
		}
		fmt.Printf(`{"loggedIn":%v,"authMethod":"claude.ai"}`, mode != "signed-out")
		return 0
	}
	if len(args) < 1 || args[0] != "--settings" {
		return 2
	}
	if mode == "stuck" {
		// A helper in claude's process group: the kill must reach it too.
		helper := descendant(nil)
		if os.WriteFile(os.Getenv(fakePIDEnv), []byte(strconv.Itoa(helper.Process.Pid)), 0o600) != nil {
			return 3
		}
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
	for _, mode := range []string{"ok", "stuck", "signed-out", "auth-leak"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			pidFile := filepath.Join(t.TempDir(), "helper.pid")
			env := []string{fakeClaudeEnv + "=" + mode, fakePIDEnv + "=" + pidFile}
			start := time.Now()
			err := claude.RealProbeForTest(context.Background(), bin, t.TempDir(), env)
			if mode == "signed-out" {
				assertCode(t, err, usage.CodeSignedOut)
			} else if err != nil {
				t.Fatal(err)
			}
			// "stuck" ignores /exit and is killed after the exit grace; "auth-leak"
			// is bounded by the auth WaitDelay, not by its 10 s helper.
			if time.Since(start) > 5*time.Second {
				t.Fatalf("probe took %v", time.Since(start))
			}
			if mode == "stuck" {
				assertGone(t, pidFile)
			}
		})
	}
}

// assertGone checks that the helper written to pidFile was killed with the group.
func assertGone(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) || zombie(pid) {
			return
		}
	}
	t.Fatalf("helper %d still alive after the probe", pid)
}

// zombie reports a killed process not reaped yet (Linux /proc; elsewhere false):
// the orphaned helper is reaped by init, which a container may never do.
func zombie(pid int) bool {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	// pid (comm) state ...: comm may hold spaces, the state follows the last ')'.
	_, rest, ok := strings.Cut(string(stat[strings.LastIndexByte(string(stat), ')')+1:]), " ")
	return ok && strings.HasPrefix(rest, "Z")
}
