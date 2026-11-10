package claude

import (
	"context"
	"os"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// Discovery exposes only the discovery seam to external tests.
type Discovery = discovery

// DiscoverForTest exercises discovery without spawning a process.
func DiscoverForTest(d Discovery, explicit string) (string, error) { return d.discover(explicit) }

// CandidatesForTest lists every candidate found, in order.
func CandidatesForTest(d Discovery) []string { return d.candidates() }

// Terminal is the PTY seam, for fakes.
type Terminal = terminal

// TestTiming shortens every wait of a probe.
var TestTiming = timing{
	tick:        2 * time.Millisecond,
	quiet:       40 * time.Millisecond,
	readySettle: 20 * time.Millisecond,
	panelSettle: 20 * time.Millisecond,
	exitGrace:   80 * time.Millisecond,
	killWait:    50 * time.Millisecond,
	keyGap:      time.Millisecond,
}

// WaitsView exposes the TestTiming waits the tests compare against.
type WaitsView struct{ Quiet, ExitGrace, KillWait time.Duration }

// WaitsForTest returns those waits.
func WaitsForTest() WaitsView {
	return WaitsView{Quiet: TestTiming.quiet, ExitGrace: TestTiming.exitGrace, KillWait: TestTiming.killWait}
}

// testCeiling bounds a test drive whose ctx has no deadline, so a broken test
// fails instead of hanging.
const testCeiling = 10 * time.Second

// bounded gives ctx testCeiling when it has no deadline.
func bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, testCeiling)
}

// runsOf turns "the status line ran" into the run count drive reads.
func runsOf(ready func() bool) func() int {
	return func() int {
		if ready() {
			return 1
		}
		return 0
	}
}

// statusRuns is a fake terminal that counts the status line runs of its claude.
type statusRuns interface{ StatusRuns() int }

// runsFrom reads the status line runs from a fake terminal, or none.
func runsFrom(term Terminal) func() int {
	if r, ok := term.(statusRuns); ok {
		return r.StatusRuns
	}
	return nil
}

// DriveForTest drives a fake terminal with TestTiming; the fake reports its own
// status line runs.
func DriveForTest(ctx context.Context, term Terminal, afterEsc func(context.Context) error) error {
	ctx, cancel := bounded(ctx)
	defer cancel()
	var after afterEscFunc
	if afterEsc != nil {
		after = func(ctx context.Context, _ int) error { return afterEsc(ctx) }
	}
	_, _, err := drive(ctx, term, TestTiming, runsFrom(term), after)
	return err
}

// DriveReadyForTest drives a fake terminal whose status line runs report ready.
func DriveReadyForTest(ctx context.Context, term Terminal, ready func() bool) error {
	ctx, cancel := bounded(ctx)
	defer cancel()
	_, _, err := drive(ctx, term, TestTiming, runsOf(ready), nil)
	return err
}

// DriveRunsForTest drives a fake terminal with the status line run count given.
func DriveRunsForTest(ctx context.Context, term Terminal, runs func() int) error {
	ctx, cancel := bounded(ctx)
	defer cancel()
	_, _, err := drive(ctx, term, TestTiming, runs, nil)
	return err
}

// PanelForTest drives a fake terminal and returns the normalized panel text.
func PanelForTest(ctx context.Context, term Terminal) (string, error) {
	ctx, cancel := bounded(ctx)
	defer cancel()
	panel, _, err := drive(ctx, term, TestTiming, runsFrom(term), nil)
	return panel, err
}

// CreditsForTest reads the Usage credits block of a normalized panel at now.
func CreditsForTest(panel string, now time.Time) (usage.Usage, error) {
	return mapCredits(panel, now)
}

// ProbeForTest runs a probe with fake start and auth.
func ProbeForTest(ctx context.Context, start func(bin string, args, env []string, dir string) (Terminal, error), auth func() []byte, environ []string, dir string) error {
	p := prober{
		start:   start,
		auth:    func(context.Context, string, string, []string) []byte { return auth() },
		environ: func() []string { return environ },
		timing:  TestTiming,
	}
	var started Terminal
	p.start = func(bin string, args, env []string, dir string) (terminal, error) {
		term, err := start(bin, args, env, dir)
		started = term
		return term, err
	}
	runs := func() int {
		if f := runsFrom(started); f != nil {
			return f()
		}
		return 0
	}
	_, err := p.probe(ctx, "claude", dir, "statusline-sink", runs, nil)
	return err
}

var (
	NormalizeForTest  = normalize
	ChildEnvForTest   = childEnv
	ClaudeArgsForTest = claudeArgs
)

// RealProbeForTest runs the real PTY and auth pre-check in dir, with extra
// environment for the child and TestTiming. The status line appends one line per run
// to a file of its own, which tells the probe the prompt was drawn.
func RealProbeForTest(ctx context.Context, bin, dir string, extraEnv []string) error {
	sink, err := os.CreateTemp("", "statusline-*.jsonl")
	if err != nil {
		return err
	}
	path := sink.Name()
	_ = sink.Close()
	defer func() { _ = os.Remove(path) }()
	p := systemProber()
	p.timing = TestTiming
	p.environ = func() []string { return append(os.Environ(), extraEnv...) }
	command := "cat >> " + shellQuote(path) + " && echo >> " + shellQuote(path)
	_, err = p.probe(ctx, bin, dir, command, func() int { return countRuns(path) }, nil)
	return err
}

// ParseAuthStatusForTest returns what the probe reads from `auth status`.
func ParseAuthStatusForTest(out []byte) (loggedIn, known, noQuota, credits bool, projects string) {
	s := parseAuthStatus(out)
	return s.loggedIn, s.known, s.noQuota, s.credits, s.projects
}

// ProviderForTest is a provider with a fake terminal and auth, real discovery (pass
// the binary with --claude-bin), base as the core's cache folder and self as the
// core binary path.
func ProviderForTest(start func(bin string, args, env []string, dir string) (Terminal, error), auth func() []byte, base, self string) *Provider {
	p := New()
	p.prober = prober{
		start:   start,
		auth:    func(context.Context, string, string, []string) []byte { return auth() },
		environ: func() []string { return nil },
		timing:  TestTiming,
	}
	p.base = func() (string, error) { return base, nil }
	p.self = func() (string, error) { return self, nil }
	return p
}

// RealProviderForTest is the real provider with TestTiming, base as the core's
// cache folder and extra environment for the child.
func RealProviderForTest(base string, extraEnv []string) *Provider {
	p := New()
	p.prober.timing = TestTiming
	p.prober.environ = func() []string { return append(os.Environ(), extraEnv...) }
	p.base = func() (string, error) { return base, nil }
	return p
}

var (
	StatusCommandForTest = statusCommand
	SinkPatternForTest   = sinkPattern
)

// MapSinkForTest maps what a sink file holds.
func MapSinkForTest(path string) (usage.Usage, error) { return mapUsage(readStatus(path)) }

// CleanupForTest removes the transcripts a sink file reports.
func CleanupForTest(sink, dir, projects string) {
	removeTranscripts(readStatus(sink).transcripts, dir, projects)
}

// RemoveStaleSinksForTest cleans base as a fetch does when it starts.
func RemoveStaleSinksForTest(base string, now time.Time) { removeStaleSinks(base, now) }
