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
	"sync/atomic"
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
	screenAPIKey    = "\x1b[2J\x1b[H\x1b[1mDetected\x1b[12Ga\x1b[14Gcustom\x1b[21GAPI\x1b[25Gkey in your environment\x1b[22m\r\nANTHROPIC_API_KEY: sk-ant-...-synthetic\r\nDo you want to use this API key?\r\n  Yes\r\n❯ No (recommended)\r\n"
	screenPanel     = "\x1b[2J\x1b[H Usage\r\n Current\x1b[1Csession\r\n \x1b[38;5;2m███\x1b[0m 12%\x1b[1Cused\r\n Resets 3pm (UTC)\r\n"
	// A plan billed through usage credits (#249, 2.1.319): no session or week windows,
	// only the credits bar, whose "NN% used" is what tells the panel has opened.
	screenCreditsPanel = "\x1b[2J\x1b[H Usage\r\n Usage\x1b[1Ccredits\r\n \x1b[38;5;2m█\x1b[0m 9%\x1b[1Cused\r\n $1.00 / $10.00 spent · Resets Dec 1 (UTC)\r\n"
)

// fake is a scripted claude: react answers every key the probe types.
type fake struct {
	mu       sync.Mutex
	pr       *io.PipeReader
	pw       *io.PipeWriter
	keys     []string
	killed   bool
	killedAt time.Time
	noEcho   bool // a dialog: typed text is not shown
	react    func(f *fake, key string)
	// echoDelay delays the echo of a typed command (a slow machine).
	echoDelay time.Duration
	// onKey runs on every key, before the reaction.
	onKey func()
	// hang, when set, keeps Wait blocked until closed: a tree that does not end.
	hang chan struct{}
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
func (f *fake) Wait() error {
	if f.hang != nil {
		<-f.hang
	}
	return nil
}

func (f *fake) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.keys = append(f.keys, string(p))
	echo := !f.noEcho && strings.HasPrefix(string(p), "/")
	delay, onKey := f.echoDelay, f.onKey
	f.mu.Unlock()
	if onKey != nil {
		onKey()
	}
	if echo {
		// The prompt shows a typed command, with its suggestion below.
		go func() {
			time.Sleep(delay)
			f.print("\x1b[2K\r> " + string(p) + "\r\n  " + string(p) + "  Show something\r\n")
		}()
	}
	if f.react != nil {
		f.react(f, string(p))
	}
	return len(p), nil
}

func (f *fake) Kill() error {
	f.mu.Lock()
	f.killed, f.killedAt = true, time.Now()
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
func signedIn(f *fake, key string) { showPanel(f, key, screenPanel) }

// showPanel answers /usage with panel and /exit by exiting.
func showPanel(f *fake, key, panel string) {
	last := f.typed()
	prev := ""
	if len(last) > 1 {
		prev = last[len(last)-2]
	}
	switch {
	case key == "\r" && prev == "/usage":
		go f.print(panel)
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

func assertReason(t *testing.T, err error, want string) {
	t.Helper()
	var e *usage.Error
	if !errors.As(err, &e) || e.Reason != want {
		t.Fatalf("err = %#v; want reason %q", err, want)
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
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	assertCode(t, claude.DriveForTest(ctx, f, nil), usage.CodeNotReady)
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

// #228: the API key question is never answered, not even by the /usage meant for the
// prompt (its Enter would store "No" in the user's Claude Code), also after trust.
func TestDriveLeavesTheAPIKeyQuestion(t *testing.T) {
	t.Parallel()
	t.Run("at start", func(t *testing.T) {
		t.Parallel()
		f := newFake(screenAPIKey, nil)
		err := claude.DriveForTest(context.Background(), f, nil)
		assertCode(t, err, usage.CodeUnavailable)
		assertReason(t, err, claude.ReasonAPIKeyQuestion)
		assertKeys(t, f)
	})
	t.Run("after trust", func(t *testing.T) {
		t.Parallel()
		f := newFake(screenTrust, func(f *fake, key string) {
			if key == "\r" {
				go f.print(screenAPIKey)
			}
		})
		assertCode(t, claude.DriveForTest(context.Background(), f, nil), usage.CodeUnavailable)
		assertKeys(t, f, "\x1b[B", "\r")
	})
}

// #228: a quiet screen that does not show the typed /usage is a dialog, not the prompt:
// no Enter (it would confirm the dialog's preselected answer), not_ready.
func TestDriveNoEnterIntoAnUnknownDialog(t *testing.T) {
	t.Parallel()
	f := newFake("\x1b[2J\x1b[HSomething new?\r\n❯ 1. Accept\r\n  2. Decline\r\n", nil)
	f.noEcho = true
	assertCode(t, claude.DriveForTest(context.Background(), f, nil), usage.CodeNotReady)
	assertKeys(t, f, "/usage")
}

func TestDriveSignedOutWhileWaitingForTheEcho(t *testing.T) {
	t.Parallel()
	f := newFake(screenPrompt, func(f *fake, key string) {
		if key == "/usage" {
			go f.print(screenSignedOut)
		}
	})
	f.noEcho = true
	assertCode(t, claude.DriveForTest(context.Background(), f, nil), usage.CodeSignedOut)
	assertKeys(t, f, "/usage")
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
	// The macOS-over-SSH case: the usage tab opens without windows. The panel is
	// awaited up to the deadline, then not_ready (not a timeout: no panel to settle).
	f := newFake(screenPrompt, func(f *fake, key string) {
		if key == "\r" {
			go f.print("\x1b[2J\x1b[H Session\r\n Total cost: $0.0000\r\n")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	err := claude.DriveForTest(ctx, f, nil)
	assertCode(t, err, usage.CodeNotReady)
	if !strings.Contains(err.Error(), "no usage panel within") {
		t.Fatalf("err = %v", err)
	}
}

// #266: a pause in a slow start is not the prompt. Nothing is typed until Claude ran
// the status line, then the read succeeds.
func TestDriveWaitsForTheStatusLineThroughASlowStart(t *testing.T) {
	t.Parallel()
	var ran, early atomic.Bool
	f := newFake("\x1b[2J\x1b[HLoading", signedIn)
	f.onKey = func() {
		if !ran.Load() {
			early.Store(true)
		}
	}
	go func() {
		// Silent well past quiet, short of the fallback: a slow start.
		time.Sleep(2 * claude.WaitsForTest().Quiet)
		f.print(screenPrompt)
		ran.Store(true)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := claude.DriveReadyForTest(ctx, f, ran.Load); err != nil {
		t.Fatal(err)
	}
	if early.Load() {
		t.Fatal("typed before the prompt")
	}
	assertKeys(t, f, "/usage", "\r", "\x1b", "/exit", "\r")
}

// #266: once the status line ran, a slow echo is awaited, not taken for a dialog.
func TestDriveWaitsForASlowEcho(t *testing.T) {
	t.Parallel()
	f := newFake(screenPrompt, signedIn)
	f.echoDelay = 2 * claude.WaitsForTest().EchoTimeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := claude.DriveReadyForTest(ctx, f, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, f, "/usage", "\r", "\x1b", "/exit", "\r")
}

// Without a status line run, silence counts as the prompt only after the fallback wait.
func TestDriveFallsBackToSilence(t *testing.T) {
	t.Parallel()
	f := newFake(screenPrompt, signedIn)
	start := time.Now()
	var firstKey time.Duration
	f.onKey = func() {
		if firstKey == 0 {
			firstKey = time.Since(start)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := claude.DriveForTest(ctx, f, nil); err != nil {
		t.Fatal(err)
	}
	if firstKey < claude.WaitsForTest().ReadyFallback {
		t.Fatalf("typed after %v, before the fallback %v", firstKey, claude.WaitsForTest().ReadyFallback)
	}
}

// #266: the core answers within its deadline even when the process tree never ends.
func TestDriveAnswersWithinTheDeadline(t *testing.T) {
	t.Parallel()
	f := newFake("", nil)
	f.hang = make(chan struct{})
	defer close(f.hang)
	const deadline = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	start := time.Now()
	assertCode(t, claude.DriveForTest(ctx, f, nil), usage.CodeNotReady)
	if elapsed := time.Since(start); elapsed > deadline+30*time.Millisecond {
		t.Fatalf("answered after %v; deadline %v", elapsed, deadline)
	}
	if !f.killed {
		t.Fatal("process not killed")
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
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	assertCode(t, claude.DriveReadyForTest(ctx, f, func() bool { return true }), usage.CodeTimeout)
}

// #266: the exit grace never reaches into the time kept for the kill, typing /exit
// included (keyGap well above the tick here), and the core answers by the deadline
// even when /exit is ignored and the process tree never ends.
func TestDriveLeavesWithinTheDeadlineAfterARead(t *testing.T) {
	t.Parallel()
	f := newFake(screenPrompt, func(f *fake, key string) {
		if key == "\r" && len(f.typed()) == 2 {
			go f.print(screenPanel)
		}
	})
	f.hang = make(chan struct{})
	defer close(f.hang)
	const deadline, keyGap = 1500 * time.Millisecond, 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	start := time.Now()
	// An exit grace longer than the deadline: always cut by it.
	reserve, err := claude.DriveSlowKeysForTest(ctx, f, func() bool { return true }, keyGap, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Margins of half a keyGap absorb scheduler delays; the bug fixed here overruns by
	// a whole keyGap.
	if elapsed := time.Since(start); elapsed > deadline+keyGap/2 {
		t.Fatalf("answered after %v; deadline %v", elapsed, deadline)
	}
	killed := f.killedAt.Sub(start)
	if f.killedAt.IsZero() || killed > deadline-reserve+keyGap/2 {
		t.Fatalf("killed after %v; the grace ends at %v", killed, deadline-reserve)
	}
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
		reason  string
	}{
		{"signed in", `{"loggedIn":true,"authMethod":"claude.ai"}`, true, "", ""},
		{"signed out", `{"loggedIn":false}`, false, usage.CodeSignedOut, ""},
		{"API key", `{"loggedIn":true,"authMethod":"api_key"}`, false, usage.CodeUnavailable, claude.ReasonNoSubscription},
		{"cloud provider", `{"loggedIn":true,"authMethod":"third_party","apiProvider":"vertex"}`, false, usage.CodeUnavailable, claude.ReasonNoSubscription},
		{"auth status unusable", "not json", true, "", ""},
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
				assertReason(t, err, tt.reason)
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
	got := claude.ChildEnvForTest([]string{
		"PATH=/usr/bin", "CLAUDECODE=1", "CLAUDE_CODE_SSE_PORT=1", "CLAUDE_CODE_ENTRYPOINT=cli",
		"CLAUDE_CODE_SESSION_ID=synthetic", "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CODE_SESSION_ATTENDED=1",
		"CLAUDE_CODE_EXECPATH=/synthetic/claude", "CLAUDE_PID=1", "CLAUDE_EFFORT=high",
		"CLAUDE_CODE_MESSAGING_SOCKET=/synthetic/sock", "CLAUDE_CODE_MESSAGING_TOKEN=synthetic",
		"CLAUDE_CODE_BRIDGE_SESSION_ID=synthetic", "CLAUDE_CODE_REMOTE=true", "CLAUDE_CODE_REMOTE_SESSION_ID=synthetic",
		"CLAUDE_JOB_DIR=/synthetic/job", "AI_AGENT=claude-code",
		"TERM=dumb", "Term=dumb", "claudecode=1", "CLAUDE_CONFIG_DIR=/synthetic",
		// Windows drive entries and malformed entries pass through untouched.
		"=C:=C:\\synthetic", "NOEQUALS",
		// #228: the user's configuration reaches the child.
		"CLAUDE_CODE_USE_BEDROCK=1", "CLAUDE_CODE_SKIP_BEDROCK_AUTH=1", "CLAUDE_CODE_OAUTH_TOKEN=synthetic",
		"CLAUDE_CODE_CLIENT_CERT=/synthetic/cert.pem", "CLAUDE_CODE_GIT_BASH_PATH=/synthetic/bash",
		"ANTHROPIC_API_KEY=synthetic",
	})
	want := []string{
		"PATH=/usr/bin", "CLAUDE_CONFIG_DIR=/synthetic", "=C:=C:\\synthetic", "NOEQUALS",
		"CLAUDE_CODE_USE_BEDROCK=1", "CLAUDE_CODE_SKIP_BEDROCK_AUTH=1", "CLAUDE_CODE_OAUTH_TOKEN=synthetic",
		"CLAUDE_CODE_CLIENT_CERT=/synthetic/cert.pem", "CLAUDE_CODE_GIT_BASH_PATH=/synthetic/bash",
		"ANTHROPIC_API_KEY=synthetic", "TERM=xterm-256color",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("env = %q; want %q", got, want)
	}
}

func TestParseAuthStatus(t *testing.T) {
	t.Parallel()
	// An absolute path on the host running the test.
	projects := filepath.Join(t.TempDir(), "synthetic", ".claude", "projects")
	quoted, err := json.Marshal(projects)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		in                                string
		loggedIn, known, noQuota, credits bool
		projects                          string
	}{
		{`{"loggedIn":true,"authMethod":"claude.ai","email":"synthetic@example.invalid","projectsDirectory":` + string(quoted) + `}`, true, true, false, false, projects},
		{`{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"pro"}`, true, true, false, false, ""},
		{`{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"enterprise","planTier":"other"}`, true, true, false, true, ""},
		{`{"loggedIn":false,"subscriptionType":"enterprise"}`, false, true, false, false, ""},
		{`{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty"}`, true, true, false, false, ""},
		{`{"loggedIn":true,"authMethod":"api_key","apiProvider":"firstParty"}`, true, true, true, false, ""},
		{`{"loggedIn":true,"authMethod":"third_party","apiProvider":"bedrock"}`, true, true, true, false, ""},
		{`{"loggedIn":true,"authMethod":"something_new"}`, true, true, false, false, ""},
		{`{"loggedIn":false,"authMethod":"api_key"}`, false, true, false, false, ""},
		{`{"loggedIn":true,"projectsDirectory":"relative/projects"}`, true, true, false, false, ""},
		{`{"loggedIn":false}`, false, true, false, false, ""},
		{`{"authMethod":"claude.ai","projectsDirectory":"/synthetic"}`, false, false, false, false, ""},
		{``, false, false, false, false, ""},
		{`Logged in`, false, false, false, false, ""},
	}
	for _, tt := range tests {
		loggedIn, known, noQuota, credits, projects := claude.ParseAuthStatusForTest([]byte(tt.in))
		if loggedIn != tt.loggedIn || known != tt.known || noQuota != tt.noQuota || credits != tt.credits || projects != tt.projects {
			t.Errorf("parseAuthStatus(%q) = %v, %v, %v, %v, %q; want %v, %v, %v, %v, %q", tt.in, loggedIn, known, noQuota, credits, projects, tt.loggedIn, tt.known, tt.noQuota, tt.credits, tt.projects)
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
