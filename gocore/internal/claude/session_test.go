package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/claude"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// Synthetic screens, in the shape Claude Code draws them (escapes, cursor moves that
// drop spaces); no real output or values.
const (
	screenPrompt    = "\x1b[2J\x1b[H\x1b[?25l╭──────╮\r\n│ \x1b[1m>\x1b[0m \x1b[7m \x1b[0m │\r\n╰──────╯\r\n"
	screenTrust     = "\x1b[2J\x1b[HQuick\x1b[1Csafety\x1b[1Ccheck: Is this a project you created or one you trust?\r\n❯ 1. Yes, I trust this folder\r\n  2. No, exit\r\n"
	screenSignedOut = "\x1b[2J\x1b[H> \r\n\x1b[2mNot logged in · Run /login\x1b[0m\r\n"
	screenTheme     = "\x1b[2J\x1b[HLet's get started.\r\nChoose the text style that looks best with your terminal\r\n"
	screenPanel     = "\x1b[2J\x1b[H Usage\r\n Current\x1b[1Csession\r\n \x1b[38;5;2m███\x1b[0m 12%\x1b[1Cused\r\n Resets 3pm (UTC)\r\n"
)

// fake is a scripted claude: react answers every key the probe types.
type fake struct {
	mu     sync.Mutex
	pr     *io.PipeReader
	pw     *io.PipeWriter
	keys   []string
	killed bool
	react  func(f *fake, key string)
}

func newFake(first string, react func(f *fake, key string)) *fake {
	pr, pw := io.Pipe()
	f := &fake{pr: pr, pw: pw, react: react}
	if first != "" {
		go f.print(first)
	}
	return f
}

func (f *fake) print(s string)             { _, _ = f.pw.Write([]byte(s)) }
func (f *fake) exit()                      { _ = f.pw.Close() }
func (f *fake) Read(p []byte) (int, error) { return f.pr.Read(p) }
func (f *fake) Wait() error                { return nil }

func (f *fake) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.keys = append(f.keys, string(p))
	f.mu.Unlock()
	if f.react != nil {
		f.react(f, string(p))
	}
	return len(p), nil
}

func (f *fake) Kill() error {
	f.mu.Lock()
	f.killed = true
	f.mu.Unlock()
	f.exit()
	return nil
}

func (f *fake) typed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.keys)
}

// signedIn reacts like a signed-in claude: the panel after /usage, and /exit ends it.
func signedIn(f *fake, key string) {
	last := f.typed()
	prev := ""
	if len(last) > 1 {
		prev = last[len(last)-2]
	}
	switch {
	case key == "\r" && prev == "/usage":
		go f.print(screenPanel)
	case key == "\r" && prev == "/exit":
		go f.exit()
	}
}

func assertCode(t *testing.T, err error, want usage.Code) {
	t.Helper()
	var e *usage.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("err = %#v; want code %q", err, want)
	}
}

func assertKeys(t *testing.T, f *fake, want ...string) {
	t.Helper()
	if got := f.typed(); !slices.Equal(got, want) {
		t.Fatalf("typed %q; want %q", got, want)
	}
}

func TestDriveReadsThePanelAndLeaves(t *testing.T) {
	t.Parallel()
	f := newFake(screenPrompt, signedIn)
	var keysAtEsc []string
	err := claude.DriveForTest(context.Background(), f, func(context.Context) error {
		keysAtEsc = f.typed()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// afterEsc runs with the panel closed and before /exit.
	if !slices.Equal(keysAtEsc, []string{"/usage", "\r", "\x1b"}) {
		t.Fatalf("keys at afterEsc = %q", keysAtEsc)
	}
	assertKeys(t, f, "/usage", "\r", "\x1b", "/exit", "\r")
}

func TestDriveAnswersTheTrustPromptOnce(t *testing.T) {
	t.Parallel()
	f := newFake(screenTrust, func(f *fake, key string) {
		if key == "\r" && len(f.typed()) == 2 {
			go f.print(screenPrompt)
			return
		}
		signedIn(f, key)
	})
	if err := claude.DriveForTest(context.Background(), f, nil); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, f, "\x1b[B", "\r", "/usage", "\r", "\x1b", "/exit", "\r")
}

func TestDriveAnswersTrustOnlyOnce(t *testing.T) {
	t.Parallel()
	// The answer does not take: the prompt is drawn again. No second answer; /usage
	// then opens no panel.
	f := newFake(screenTrust, func(f *fake, key string) {
		if key == "\r" {
			go f.print(screenTrust)
		}
	})
	assertCode(t, claude.DriveForTest(context.Background(), f, nil), usage.CodeNotReady)
	assertKeys(t, f, "\x1b[B", "\r", "/usage", "\r")
	if !f.killed {
		t.Fatal("process not killed")
	}
}

func TestDriveSignedOut(t *testing.T) {
	t.Parallel()
	for name, screen := range map[string]string{"footer": screenSignedOut, "theme": screenTheme, "login": "Select login method:\r\n"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(screen, nil)
			assertCode(t, claude.DriveForTest(context.Background(), f, nil), usage.CodeSignedOut)
			// Nothing typed: never /usage on a signed-out claude, never /exit after a failure.
			assertKeys(t, f)
		})
	}
}

func TestDriveSignedOutInThePanel(t *testing.T) {
	t.Parallel()
	f := newFake(screenPrompt, func(f *fake, key string) {
		if key == "\r" {
			go f.print(screenSignedOut)
		}
	})
	assertCode(t, claude.DriveForTest(context.Background(), f, nil), usage.CodeSignedOut)
}

func TestDriveNoPanelIsNotReady(t *testing.T) {
	t.Parallel()
	// The macOS-over-SSH case: the usage tab opens without windows.
	f := newFake(screenPrompt, func(f *fake, key string) {
		if key == "\r" {
			go f.print("\x1b[2J\x1b[H Session\r\n Total cost: $0.0000\r\n")
		}
	})
	start := time.Now()
	assertCode(t, claude.DriveForTest(context.Background(), f, nil), usage.CodeNotReady)
	if time.Since(start) < claude.TestPanelTimeout {
		t.Fatal("gave up before the panel timeout")
	}
}

func TestDriveEarlyExit(t *testing.T) {
	t.Parallel()
	f := newFake("", nil)
	go func() { f.print("starting"); f.exit() }()
	assertCode(t, claude.DriveForTest(context.Background(), f, nil), usage.CodeUnavailable)
}

func TestDriveTimeout(t *testing.T) {
	t.Parallel()
	// No output at all: never quiet, so never ready; the probe deadline ends it as an
	// unknown blocking screen.
	f := newFake("", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	assertCode(t, claude.DriveForTest(ctx, f, nil), usage.CodeNotReady)
	if !f.killed {
		t.Fatal("process not killed")
	}
}

func TestDriveTimeoutInThePanel(t *testing.T) {
	t.Parallel()
	// A panel that never settles: output keeps coming until the probe deadline.
	f := newFake(screenPrompt, func(f *fake, key string) {
		if key == "\r" {
			go func() {
				for range 100 {
					f.print("\x1b[H" + screenPanel)
					time.Sleep(5 * time.Millisecond)
				}
			}()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	assertCode(t, claude.DriveForTest(ctx, f, nil), usage.CodeTimeout)
}

func TestDriveKillsWhenExitIsIgnored(t *testing.T) {
	t.Parallel()
	f := newFake(screenPrompt, func(f *fake, key string) {
		if key == "\r" && len(f.typed()) == 2 {
			go f.print(screenPanel)
		}
	})
	if err := claude.DriveForTest(context.Background(), f, nil); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, f, "/usage", "\r", "\x1b", "/exit", "\r")
	if !f.killed {
		t.Fatal("process not killed after the exit grace")
	}
}

func TestDriveAfterEscError(t *testing.T) {
	t.Parallel()
	f := newFake(screenPrompt, signedIn)
	want := &usage.Error{Code: usage.CodeFormatDrift, Message: "synthetic"}
	if err := claude.DriveForTest(context.Background(), f, func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatalf("err = %v; want %v", err, want)
	}
	// No /exit after a failure: the tree is killed.
	assertKeys(t, f, "/usage", "\r", "\x1b")
}

func TestProbe(t *testing.T) {
	t.Parallel()
	environ := []string{"PATH=/usr/bin", "CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "TERM=dumb", "HOME=/home/synthetic"}
	tests := []struct {
		name    string
		auth    string
		started bool
		code    usage.Code
	}{
		{"signed in", `{"loggedIn":true,"authMethod":"claude.ai"}`, true, ""},
		{"signed out", `{"loggedIn":false}`, false, usage.CodeSignedOut},
		{"auth status unusable", "not json", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			started := false
			start := func(bin string, args, env []string, cwd string) (claude.Terminal, error) {
				started = true
				if cwd != dir || bin != "claude" || !slices.Equal(args, claude.ClaudeArgsForTest("statusline-sink")) {
					t.Errorf("start(%q, %q, %q)", bin, args, cwd)
				}
				if !slices.Equal(env, claude.ChildEnvForTest(environ)) {
					t.Errorf("env = %q", env)
				}
				return newFake(screenPrompt, signedIn), nil
			}
			err := claude.ProbeForTest(context.Background(), start, func() []byte { return []byte(tt.auth) }, environ, dir)
			if tt.code == "" && err != nil {
				t.Fatal(err)
			}
			if tt.code != "" {
				assertCode(t, err, tt.code)
			}
			if started != tt.started {
				t.Fatalf("started = %v; want %v", started, tt.started)
			}
		})
	}
}

func TestProbeRefusesAFolderWithFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "planted"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	start := func(string, []string, []string, string) (claude.Terminal, error) {
		t.Error("started in a folder with files")
		return nil, errors.New("unexpected")
	}
	err := claude.ProbeForTest(context.Background(), start, func() []byte { return nil }, nil, dir)
	assertCode(t, err, usage.CodeUnavailable)
}

func TestClaudeArgs(t *testing.T) {
	t.Parallel()
	args := claude.ClaudeArgsForTest(`/opt/core "sink"`)
	if len(args) != 5 || args[0] != "--settings" || !slices.Equal(args[2:], []string{"--strict-mcp-config", "--setting-sources", "local"}) {
		t.Fatalf("args = %q", args)
	}
	var settings map[string]map[string]string
	if err := json.Unmarshal([]byte(args[1]), &settings); err != nil {
		t.Fatal(err)
	}
	if settings["statusLine"]["type"] != "command" || settings["statusLine"]["command"] != `/opt/core "sink"` || len(settings) != 1 {
		t.Fatalf("settings = %v", settings)
	}
	for _, arg := range args {
		if strings.Contains(arg, "disableAllHooks") || arg == "--bare" {
			t.Fatalf("forbidden flag in %q", args)
		}
	}
}

func TestChildEnv(t *testing.T) {
	t.Parallel()
	got := claude.ChildEnvForTest([]string{"PATH=/usr/bin", "CLAUDECODE=1", "CLAUDE_CODE_SSE_PORT=1", "TERM=dumb", "CLAUDE_CONFIG_DIR=/synthetic"})
	want := []string{"PATH=/usr/bin", "CLAUDE_CONFIG_DIR=/synthetic", "TERM=xterm-256color"}
	if !slices.Equal(got, want) {
		t.Fatalf("env = %q; want %q", got, want)
	}
}

func TestParseAuthStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in              string
		loggedIn, known bool
		projects        string
	}{
		{`{"loggedIn":true,"authMethod":"claude.ai","email":"synthetic@example.invalid","projectsDirectory":"/home/synthetic/.claude/projects"}`, true, true, "/home/synthetic/.claude/projects"},
		{`{"loggedIn":true,"projectsDirectory":"relative/projects"}`, true, true, ""},
		{`{"loggedIn":false}`, false, true, ""},
		{`{"authMethod":"claude.ai","projectsDirectory":"/synthetic"}`, false, false, ""},
		{``, false, false, ""},
		{`Logged in`, false, false, ""},
	}
	for _, tt := range tests {
		if loggedIn, known, projects := claude.ParseAuthStatusForTest([]byte(tt.in)); loggedIn != tt.loggedIn || known != tt.known || projects != tt.projects {
			t.Errorf("parseAuthStatus(%q) = %v, %v, %q; want %v, %v, %q", tt.in, loggedIn, known, projects, tt.loggedIn, tt.known, tt.projects)
		}
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	got := claude.NormalizeForTest([]byte("\x1b]0;title\x07 Current\x1b[1Csession\r\n\x1b[38;5;2m12%\x1b[0m used \x1b(B"))
	if got != "currentsession12%used" {
		t.Fatalf("normalize = %q", got)
	}
}
