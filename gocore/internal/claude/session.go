package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// PTY size and keys. The probe types only these, plus the two characters of the
// folder trust answer: never a prompt, so no model request on the user's plan.
const (
	ptyRows, ptyCols = 50, 140
	keyDown          = "\x1b[B"
	keyEnter         = "\r"
	keyEsc           = "\x1b"
	cmdUsage         = "/usage"
	cmdExit          = "/exit"
)

// Markers are matched on the output since the last key, with escape sequences and
// all white space removed and lower-cased: cursor moves can drop every space
// (`Currentsession`, `12%used`). Verified on Linux and macOS (#141, #183) and under
// ConPTY on Windows 11 (#223: trust prompt answered, panel found).
var (
	markersSignedOut = []string{"notloggedin", "selectloginmethod", "choosethetextstyle"}
	markersTrust     = []string{"quicksafetycheck", "itrustthisfolder"}
	// The question about an API key in the environment (seen on 2.1.319). Its default
	// answer is No, and any Enter typed into it stores that answer in the user's
	// Claude Code (#228): the probe types nothing there and leaves.
	markersAPIKey = []string{"detectedacustomapikey", "doyouwanttousethisapikey"}
	markerPanel   = "%used"
)

// timing holds the waits of a probe; tests shorten them.
type timing struct {
	tick         time.Duration // how often the output is looked at
	quiet        time.Duration // no output for this long: the prompt is ready
	panelSettle  time.Duration // no output for this long after the panel showed
	panelTimeout time.Duration // no panel this long after /usage: not_ready
	escSettle    time.Duration // longest wait for the panel to close
	exitGrace    time.Duration // time /exit gets before the kill
	keyGap       time.Duration // between a command and its Enter
}

var defaultTiming = timing{
	tick:         100 * time.Millisecond,
	quiet:        1200 * time.Millisecond,
	panelSettle:  700 * time.Millisecond,
	panelTimeout: 12 * time.Second,
	escSettle:    2 * time.Second,
	exitGrace:    5 * time.Second,
	keyGap:       300 * time.Millisecond,
}

// probeTimeout bounds a whole probe, auth pre-check included.
const probeTimeout = 25 * time.Second

// terminal is the child in its PTY: Read is its output, Write sends keys.
type terminal interface {
	io.ReadWriter
	// Kill ends the whole process tree; Wait then returns once it is gone.
	Kill() error
	Wait() error
}

type startFunc func(bin string, args, env []string, dir string) (terminal, error)

// authFunc runs `claude auth status` and returns its stdout (nil when it failed).
type authFunc func(ctx context.Context, bin, dir string, env []string) []byte

type prober struct {
	start   startFunc
	auth    authFunc
	environ func() []string
	timing  timing
}

func notReady(message string) *usage.Error {
	return &usage.Error{Code: usage.CodeNotReady, Message: message}
}

func signedOut() *usage.Error {
	return &usage.Error{Code: usage.CodeSignedOut, Message: "Claude Code is not signed in; run `claude` and sign in"}
}

// probe runs one `claude` session in dir up to the /usage panel and out again.
// statusLine is the command Claude runs as its status line; afterEsc runs once the
// panel is closed, before /exit. It returns Claude's projects folder as `auth status`
// reported it ("" when unknown), also with an error: the transcript cleanup needs it.
func (p prober) probe(ctx context.Context, bin, dir, statusLine string, afterEsc func(context.Context) error) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	// The trust prompt is answered only for this folder: it must hold nothing to trust.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", unavailable("claude probe folder: " + err.Error())
	}
	if len(entries) > 0 {
		return "", unavailable("claude probe folder is not empty: " + dir)
	}
	env := childEnv(p.environ())
	status := parseAuthStatus(p.auth(ctx, bin, dir, env))
	if status.known && !status.loggedIn {
		return status.projects, signedOut()
	}
	if status.noQuota {
		return status.projects, unavailable("Claude Code is signed in with an API key or a cloud provider, which has no subscription quota")
	}
	term, err := p.start(bin, claudeArgs(statusLine), env, dir)
	if err != nil {
		return status.projects, unavailable("cannot start claude: " + err.Error())
	}
	return status.projects, drive(ctx, term, p.timing, afterEsc)
}

// cacheBase is the core's own cache folder, <UserCacheDir>/aipermiler.
func cacheBase() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", unavailable("locate user cache directory: " + err.Error())
	}
	return filepath.Join(base, buildinfo.Name), nil
}

// probeDir is the fixed working folder of the probes, inside base.
func probeDir(base string) (string, error) {
	dir := filepath.Join(base, "claude-probe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", unavailable("create claude probe folder: " + err.Error())
	}
	return dir, nil
}

// claudeArgs: our own status line through --settings (nothing written to the user's
// settings), no MCP servers, only local settings (user hooks and plugins stay out).
// Never disableAllHooks (it turns the status line off) nor --bare (no OAuth login).
func claudeArgs(statusLine string) []string {
	settings, _ := json.Marshal(map[string]any{"statusLine": map[string]string{"type": "command", "command": statusLine}})
	return []string{"--settings", string(settings), "--strict-mcp-config", "--setting-sources", "local"}
}

// childEnv drops the markers a parent Claude Code session leaves (they change the
// child's behaviour) and sets the terminal type. Only those markers go: every other
// variable is the user's configuration (provider, sign-in, gateway, mTLS, Git Bash
// path...) and must reach the child, or it would read another sign-in than the user's
// Claude Code (#228).
func childEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		// Windows names are case-insensitive; Claude Code sets the markers in upper case.
		if strings.EqualFold(key, "TERM") || sessionMarkers[strings.ToUpper(key)] {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "TERM=xterm-256color")
}

// sessionMarkers: the variables a running Claude Code session sets for the processes
// it starts, not configuration a user sets: documented as "set by Claude Code" on
// code.claude.com/docs/en/env-vars, or seen in its subprocesses on 2.1.302-2.1.319
// (ENTRYPOINT, SSE_PORT, SESSION_ATTENDED, EXECPATH, AI_AGENT).
var sessionMarkers = map[string]bool{
	"CLAUDECODE":                    true,
	"CLAUDE_CODE_ENTRYPOINT":        true,
	"CLAUDE_CODE_SSE_PORT":          true,
	"CLAUDE_CODE_SESSION_ID":        true,
	"CLAUDE_CODE_CHILD_SESSION":     true,
	"CLAUDE_CODE_SESSION_ATTENDED":  true,
	"CLAUDE_CODE_EXECPATH":          true,
	"CLAUDE_CODE_MESSAGING_SOCKET":  true,
	"CLAUDE_CODE_MESSAGING_TOKEN":   true,
	"CLAUDE_CODE_BRIDGE_SESSION_ID": true,
	"CLAUDE_CODE_REMOTE":            true,
	"CLAUDE_CODE_REMOTE_SESSION_ID": true,
	"CLAUDE_JOB_DIR":                true,
	"CLAUDE_PID":                    true,
	"CLAUDE_EFFORT":                 true,
	"AI_AGENT":                      true,
}

// authStatus is what the probe reads from `claude auth status`: whether it is signed
// in, how, and where its transcripts live. Email and organisation are never read.
type authStatus struct {
	loggedIn bool
	known    bool   // false when the output is not usable: the PTY markers then decide
	noQuota  bool   // signed in without a subscription: no rate_limits to read
	projects string // absolute projects folder, "" when not reported
}

// noQuotaMethods are the authMethod values without subscription quotas (verified on
// 2.1.316, #228). claude.ai, oauth_token (`claude setup-token`) and any unknown
// value run the probe.
var noQuotaMethods = map[string]bool{"api_key": true, "third_party": true}

func parseAuthStatus(out []byte) authStatus {
	var status struct {
		LoggedIn *bool  `json:"loggedIn"`
		Method   string `json:"authMethod"`
		Projects string `json:"projectsDirectory"`
	}
	if json.Unmarshal(out, &status) != nil || status.LoggedIn == nil {
		return authStatus{}
	}
	result := authStatus{loggedIn: *status.LoggedIn, known: true, noQuota: *status.LoggedIn && noQuotaMethods[status.Method]}
	if filepath.IsAbs(status.Projects) {
		result.projects = status.Projects
	}
	return result
}

var escapes = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b.`)

// normalize strips escape sequences, white space and control characters.
func normalize(raw []byte) string {
	text := escapes.ReplaceAllString(string(raw), "")
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, text)
}

var errExited = errors.New("claude exited")

// maxOutput caps the output kept since the last key.
const maxOutput = 1 << 20

type driver struct {
	term   terminal
	t      timing
	out    chan []byte
	raw    []byte // output since the last key
	last   time.Time
	exited bool
}

// drive takes a started session to the /usage panel, closes it, runs afterEsc and
// leaves through /exit; the process tree is killed at the end in every case.
func drive(ctx context.Context, term terminal, t timing, afterEsc func(context.Context) error) error {
	d := &driver{term: term, t: t, out: make(chan []byte, 64)}
	go d.pump()
	err := d.run(ctx, afterEsc)
	d.leave(err == nil)
	return err
}

func (d *driver) pump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := d.term.Read(buf)
		if n > 0 {
			d.out <- append([]byte(nil), buf[:n]...)
		}
		if err != nil {
			close(d.out)
			return
		}
	}
}

// wait takes in the output that arrives within one tick.
func (d *driver) wait(ctx context.Context) error {
	timer := time.NewTimer(d.t.tick)
	defer timer.Stop()
	select {
	case chunk, ok := <-d.out:
		if !ok {
			d.exited = true
			return errExited
		}
		if len(d.raw)+len(chunk) <= maxOutput {
			d.raw = append(d.raw, chunk...)
		}
		d.last = time.Now()
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return nil
}

func (d *driver) has(markers ...string) bool {
	text := normalize(d.raw)
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func (d *driver) quiet(span time.Duration) bool {
	return !d.last.IsZero() && time.Since(d.last) >= span
}

// send types keys, keyGap apart, and forgets the output seen so far.
func (d *driver) send(keys ...string) error {
	for i, key := range keys {
		if i > 0 {
			time.Sleep(d.t.keyGap)
		}
		if _, err := io.WriteString(d.term, key); err != nil {
			return unavailable("claude terminal: " + err.Error())
		}
	}
	d.raw, d.last = d.raw[:0], time.Time{}
	return nil
}

func (d *driver) failure(err error, where string) error {
	switch {
	case errors.Is(err, errExited):
		return unavailable("claude exited " + where)
	case errors.Is(err, context.DeadlineExceeded):
		return &usage.Error{Code: usage.CodeTimeout, Message: "claude probe timed out " + where}
	}
	return err
}

func (d *driver) run(ctx context.Context, afterEsc func(context.Context) error) error {
	trusted := false
	for ready := false; !ready; {
		if err := d.wait(ctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return notReady("claude did not reach its prompt (an unknown screen?)")
			}
			return d.failure(err, "before the prompt")
		}
		switch {
		case d.has(markersSignedOut...):
			return signedOut()
		case d.has(markersAPIKey...):
			return unavailable("Claude Code asks whether to use the API key in its environment; answer it in claude, then retry")
		case !trusted && d.has(markersTrust...):
			// Answered once, never again: the redraw after Down still carries the
			// marker. Should the answer not take, /usage opens no panel: not_ready.
			if d.quiet(d.t.quiet) {
				// "Yes, I trust this folder": only ever our own empty folder (#183).
				trusted = true
				if err := d.send(keyDown, keyEnter); err != nil {
					return err
				}
			}
		case d.quiet(d.t.quiet):
			ready = true
		}
	}
	if err := d.send(cmdUsage, keyEnter); err != nil {
		return err
	}
	sent := time.Now()
	for {
		if err := d.wait(ctx); err != nil {
			return d.failure(err, "before the usage panel")
		}
		if d.has(markersSignedOut...) {
			return signedOut()
		}
		if d.has(markerPanel) && d.quiet(d.t.panelSettle) {
			break
		}
		if time.Since(sent) > d.t.panelTimeout {
			return notReady("no usage panel after /usage")
		}
	}
	if err := d.send(keyEsc); err != nil {
		return err
	}
	// Let the panel close: /exit typed during its redraw is lost (verified on Linux,
	// 2.1.316: the exit then waited for the kill).
	for closed := time.Now(); !d.quiet(d.t.panelSettle) && time.Since(closed) < d.t.escSettle; {
		if err := d.wait(ctx); err != nil {
			return d.failure(err, "while the usage panel closed")
		}
	}
	if afterEsc != nil {
		return afterEsc(ctx)
	}
	return nil
}

// leave types /exit after a successful read and gives claude exitGrace to end by
// itself (it then finishes its own writes); the process tree is killed in any case.
func (d *driver) leave(graceful bool) {
	if graceful && !d.exited && d.send(cmdExit, keyEnter) == nil {
		timer := time.NewTimer(d.t.exitGrace)
	wait:
		for {
			select {
			case _, ok := <-d.out:
				if !ok {
					d.exited = true
					break wait
				}
			case <-timer.C:
				break wait
			}
		}
		timer.Stop()
	}
	_ = d.term.Kill()
	// Keep reading while the tree ends, so a full output buffer cannot block it.
	go func() {
		for range d.out {
		}
	}()
	_ = d.term.Wait()
}
