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

// timing holds the waits of a probe; tests shorten them. The probe waits for events
// (the status line run, the echo, the panel) up to its deadline; these are only the
// settle times around those events and the fallbacks (#266).
type timing struct {
	tick          time.Duration // how often the output is looked at
	quiet         time.Duration // fallback readiness: no output for this long...
	readyFallback time.Duration // ...once this long passed without a status line run
	readySettle   time.Duration // no output for this long after the status line ran
	panelSettle   time.Duration // no output for this long after the panel showed
	afterPanel    time.Duration // kept after the panel: escSettle, the rate_limits wait, a margin
	echoTimeout   time.Duration // fallback readiness: no /usage echoed this long, a dialog
	echoWait      time.Duration // after a status line run: no /usage echoed this long, keys lost
	escSettle     time.Duration // longest wait for the panel to close
	exitGrace     time.Duration // time /exit gets before the kill
	killWait      time.Duration // longest wait for the process tree after the kill
	keyGap        time.Duration // between a command and its Enter
}

var defaultTiming = timing{
	tick:          100 * time.Millisecond,
	quiet:         1200 * time.Millisecond,
	readyFallback: 5 * time.Second,
	readySettle:   500 * time.Millisecond,
	panelSettle:   700 * time.Millisecond,
	afterPanel:    escSettle + statusWait + 500*time.Millisecond,
	echoTimeout:   3 * time.Second,
	echoWait:      10 * time.Second,
	escSettle:     escSettle,
	exitGrace:     5 * time.Second,
	killWait:      2 * time.Second,
	keyGap:        300 * time.Millisecond,
}

// leaveReserve is the part of the probe deadline kept for leaving: the kill and the
// bounded wait for the process tree, so the core answers within its own deadline.
func (t timing) leaveReserve() time.Duration { return t.killWait + t.tick }

// escSettle is the longest wait for the panel to close (timing.escSettle).
const escSettle = 2 * time.Second

// probeTimeout bounds a whole probe, auth pre-check and leaving included.
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
// statusLine is the command Claude runs as its status line; runs counts its runs so
// far (one means Claude drew its prompt); afterEsc runs once the panel is closed,
// before /exit.
// It returns what `auth status` reported, also with an error (the transcript cleanup
// needs its projects folder), and the panel text.
func (p prober) probe(ctx context.Context, bin, dir, statusLine string, runs func() int, afterEsc func(context.Context) error) (probeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	// The trust prompt is answered only for this folder: it must hold nothing to trust.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return probeResult{}, unavailable("claude probe folder: " + err.Error())
	}
	if len(entries) > 0 {
		return probeResult{}, unavailable("claude probe folder is not empty: " + dir)
	}
	env := childEnv(p.environ())
	status := probeResult{authStatus: parseAuthStatus(p.auth(ctx, bin, dir, env))}
	if status.known && !status.loggedIn {
		return status, signedOut()
	}
	if status.noQuota {
		return status, unavailableBecause(ReasonNoSubscription, "Claude Code is signed in with an API key or a cloud provider, which has no subscription quota")
	}
	term, err := p.start(bin, claudeArgs(statusLine), env, dir)
	if err != nil {
		return status, unavailable("cannot start claude: " + err.Error())
	}
	status.panel, err = drive(ctx, term, p.timing, runs, afterEsc)
	return status, err
}

// probeResult is what one probe learned besides the status line runs.
type probeResult struct {
	authStatus
	panel string // the /usage panel as normalize returns it, "" when not reached
}

// cacheBase is the core's own cache folder, <UserCacheDir>/aipermiler.
func cacheBase() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", unavailable("locate user cache directory: " + err.Error())
	}
	return filepath.Join(base, buildinfo.Name), nil
}

// probeDirName is the probes' folder inside the core's cache folder.
const probeDirName = "claude-probe"

// ProbeDir is the folder every Claude probe runs in (not created here). Clients skip
// the sessions that ran there, e.g. in their own reading of Claude Code sessions (#180).
func ProbeDir() (string, error) {
	base, err := cacheBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, probeDirName), nil
}

// probeDir is the fixed working folder of the probes, inside base.
func probeDir(base string) (string, error) {
	dir := filepath.Join(base, probeDirName)
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
	credits  bool   // a plan that may bill through usage credits instead of quota windows
	projects string // absolute projects folder, "" when not reported
}

// noQuotaMethods are the authMethod values without subscription quotas (verified on
// 2.1.316, #228). claude.ai, oauth_token (`claude setup-token`) and any unknown
// value run the probe.
var noQuotaMethods = map[string]bool{"api_key": true, "third_party": true}

// creditsSubscriptions are the subscriptionType values seen without quota windows: the
// /usage panel shows only "Usage credits" and the status line carries no rate_limits
// (enterprise on 2.1.319, #249). The status line docs list rate_limits only for Pro,
// Max and Ultra, or behind a Claude apps gateway with a spend limit
// (https://code.claude.com/docs/en/statusline). A gateway may still send it, so the
// probe runs and this only explains a missing rate_limits.
var creditsSubscriptions = map[string]bool{"enterprise": true}

func parseAuthStatus(out []byte) authStatus {
	var status struct {
		LoggedIn     *bool  `json:"loggedIn"`
		Method       string `json:"authMethod"`
		Subscription string `json:"subscriptionType"`
		Projects     string `json:"projectsDirectory"`
	}
	if json.Unmarshal(out, &status) != nil || status.LoggedIn == nil {
		return authStatus{}
	}
	result := authStatus{
		loggedIn: *status.LoggedIn,
		known:    true,
		noQuota:  *status.LoggedIn && noQuotaMethods[status.Method],
		credits:  *status.LoggedIn && creditsSubscriptions[status.Subscription],
	}
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
	runs   func() int // status line runs so far: a new one means Claude drew its prompt
	out    chan []byte
	raw    []byte // output since the last key
	last   time.Time
	exited bool
	panel  string // normalized output of the /usage panel, kept before Esc
}

// drive takes a started session to the /usage panel, closes it, runs afterEsc and
// leaves through /exit; the process tree is killed at the end in every case. It
// returns the normalized panel text, "" when the panel was not reached. Reading stops
// leaveReserve before the deadline of ctx, which must have one, so leaving fits in it
// too (#266).
func drive(ctx context.Context, term terminal, t timing, runs func() int, afterEsc func(context.Context) error) (string, error) {
	if runs == nil {
		runs = func() int { return 0 }
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return "", unavailable("internal: claude probe started without a deadline")
	}
	d := &driver{term: term, t: t, runs: runs, out: make(chan []byte, 64)}
	go d.pump()
	runCtx, cancel := context.WithDeadline(ctx, deadline.Add(-t.leaveReserve()))
	err := d.run(runCtx, afterEsc)
	cancel()
	d.leave(err == nil, deadline)
	return d.panel, err
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
	started := time.Now()
	// The sink is new for each probe: any run means the prompt was drawn, except a
	// run seen before the trust answer, which says nothing of the prompt after it.
	trusted, signalled, baseline := false, false, 0
	for ready := false; !ready; {
		if err := d.wait(ctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return notReady("claude did not reach its prompt within " + since(started))
			}
			return d.failure(err, "before the prompt")
		}
		switch {
		case d.has(markersSignedOut...):
			return signedOut()
		case d.has(markersAPIKey...):
			return unavailableBecause(ReasonAPIKeyQuestion, "Claude Code asks whether to use the API key in its environment; answer it in claude, then retry")
		case !trusted && d.has(markersTrust...):
			// Answered once, never again: the redraw after Down still carries the
			// marker. Should the answer not take, /usage opens no panel: not_ready.
			if d.quiet(d.t.quiet) {
				// "Yes, I trust this folder": only ever our own empty folder (#183).
				trusted, signalled, baseline = true, false, d.runs()
				if err := d.send(keyDown, keyEnter); err != nil {
					return err
				}
			}
		case signalled || d.runs() > baseline:
			// Claude ran the status line: it drew its prompt. A short settle lets the
			// first frame finish (#266). Remembered: the sink is read until it said so.
			signalled = true
			ready = d.quiet(d.t.readySettle)
		case d.quiet(d.t.quiet) && time.Since(started) >= d.t.readyFallback:
			// No status line run yet: silence alone, and only after a long wait, since
			// a pause in a slow start looks the same (#266).
			ready = true
		}
	}
	// Enter only once the prompt shows /usage: a quiet screen may be a dialog instead,
	// and an Enter there would confirm its preselected answer in the user's Claude
	// Code (#228). A selection dialog does not echo the text. After a status line run
	// the prompt is there: its echo gets echoWait, for a loaded machine. Never typed
	// again: a late echo of both would send /usage/usage to Claude.
	if err := d.send(cmdUsage); err != nil {
		return err
	}
	echoLimit, guess := d.t.echoWait, ""
	if !signalled {
		echoLimit, guess = d.t.echoTimeout, " (a dialog?)"
	}
	for typed := time.Now(); !d.has(cmdUsage); {
		if time.Since(typed) > echoLimit {
			return notReady("claude did not show /usage at its prompt within " + since(typed) + guess)
		}
		if err := d.wait(ctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return notReady("claude did not show /usage at its prompt within " + since(typed))
			}
			return d.failure(err, "before the prompt showed /usage")
		}
		if d.has(markersSignedOut...) {
			return signedOut()
		}
		if d.has(markersAPIKey...) {
			// A known dialog over the prompt: said at once, not after the deadline.
			return unavailableBecause(ReasonAPIKeyQuestion, "Claude Code asks whether to use the API key in its environment; answer it in claude, then retry")
		}
	}
	// The panel is awaited up to afterPanel before the deadline: closing it and the
	// status line run with rate_limits (afterEsc) need that time once it showed.
	deadline, _ := ctx.Deadline()
	panelDeadline := deadline.Add(-d.t.afterPanel)
	if time.Until(panelDeadline) <= d.t.keyGap {
		return &usage.Error{Code: usage.CodeTimeout, Message: "claude probe timed out: no time left for the usage panel"}
	}
	time.Sleep(d.t.keyGap)
	if err := d.send(keyEnter); err != nil {
		return err
	}
	panelCtx, cancelPanel := context.WithDeadline(ctx, panelDeadline)
	defer cancelPanel()
	sent := time.Now()
	for {
		if err := d.wait(panelCtx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if d.has(markerPanel) {
					// Shown but still redrawing: what it shows is kept.
					break
				}
				// The usage tab without windows (macOS over SSH) or no tab at all.
				return notReady("no usage panel within " + since(sent) + " after /usage")
			}
			return d.failure(err, "before the usage panel")
		}
		if d.has(markersSignedOut...) {
			return signedOut()
		}
		if d.has(markerPanel) && d.quiet(d.t.panelSettle) {
			break
		}
	}
	d.panel = normalize(d.raw)
	if err := d.send(keyEsc); err != nil {
		return err
	}
	// Let the panel close: /exit typed during its redraw is lost (verified on Linux,
	// 2.1.316: the exit then waited for the kill).
	// At the deadline the panel already read is kept: afterEsc gets what came.
	for closed := time.Now(); !d.quiet(d.t.panelSettle) && time.Since(closed) < d.t.escSettle; {
		if err := d.wait(ctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return d.failure(err, "while the usage panel closed")
		}
	}
	if afterEsc != nil {
		return afterEsc(ctx)
	}
	return nil
}

// since is the time elapsed from start, in whole seconds (tenths under ten).
func since(start time.Time) string {
	elapsed := time.Since(start)
	if elapsed < 10*time.Second {
		return elapsed.Round(100 * time.Millisecond).String()
	}
	return elapsed.Round(time.Second).String()
}

// leave types /exit after a successful read and gives claude exitGrace to end by
// itself (it then finishes its own writes), never past the probe deadline less the
// time the kill needs; the process tree is killed in any case, and its end is
// awaited at most killWait and never past the deadline, so the core always answers
// in time (#266). Every bound is an absolute time: typing /exit takes keyGap too.
func (d *driver) leave(graceful bool, deadline time.Time) {
	graceEnd := minTime(time.Now().Add(d.t.keyGap+d.t.exitGrace), deadline.Add(-d.t.leaveReserve()))
	if graceful && time.Until(graceEnd) > d.t.keyGap && !d.exited && d.send(cmdExit, keyEnter) == nil {
		timer := time.NewTimer(time.Until(graceEnd))
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
	ended := make(chan struct{})
	go func() {
		_ = d.term.Wait()
		close(ended)
	}()
	timer := time.NewTimer(time.Until(minTime(time.Now().Add(d.t.killWait), deadline)))
	defer timer.Stop()
	select {
	case <-ended:
	case <-timer.C:
	}
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
