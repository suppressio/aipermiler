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
	tick:         2 * time.Millisecond,
	quiet:        40 * time.Millisecond,
	panelSettle:  20 * time.Millisecond,
	panelTimeout: 300 * time.Millisecond,
	escSettle:    100 * time.Millisecond,
	exitGrace:    80 * time.Millisecond,
	keyGap:       time.Millisecond,
}

// DriveForTest drives a fake terminal with TestTiming.
func DriveForTest(ctx context.Context, term Terminal, afterEsc func(context.Context) error) error {
	return drive(ctx, term, TestTiming, afterEsc)
}

// ProbeForTest runs a probe with fake start and auth.
func ProbeForTest(ctx context.Context, start func(bin string, args, env []string, dir string) (Terminal, error), auth func() []byte, environ []string, dir string) error {
	p := prober{
		start:   start,
		auth:    func(context.Context, string, string, []string) []byte { return auth() },
		environ: func() []string { return environ },
		timing:  TestTiming,
	}
	_, err := p.probe(ctx, "claude", dir, "statusline-sink", nil)
	return err
}

var (
	NormalizeForTest  = normalize
	ChildEnvForTest   = childEnv
	ClaudeArgsForTest = claudeArgs
)

// TestPanelTimeout is TestTiming's wait for the panel.
var TestPanelTimeout = TestTiming.panelTimeout

// RealProbeForTest runs the real PTY and auth pre-check in dir, with extra
// environment for the child and TestTiming.
func RealProbeForTest(ctx context.Context, bin, dir string, extraEnv []string) error {
	p := systemProber()
	p.timing = TestTiming
	p.environ = func() []string { return append(os.Environ(), extraEnv...) }
	_, err := p.probe(ctx, bin, dir, "true", nil)
	return err
}

// ParseAuthStatusForTest returns what the probe reads from `auth status`.
func ParseAuthStatusForTest(out []byte) (loggedIn, known bool, projects string) {
	s := parseAuthStatus(out)
	return s.loggedIn, s.known, s.projects
}

// TestStatusWait is the providers' wait for the status line in tests.
const TestStatusWait = 300 * time.Millisecond

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
	p.statusWait = TestStatusWait
	return p
}

// RealProviderForTest is the real provider with TestTiming, base as the core's
// cache folder and extra environment for the child.
func RealProviderForTest(base string, extraEnv []string) *Provider {
	p := New()
	p.prober.timing = TestTiming
	p.prober.environ = func() []string { return append(os.Environ(), extraEnv...) }
	p.base = func() (string, error) { return base, nil }
	p.statusWait = TestStatusWait
	return p
}

var (
	ShellQuoteForTest  = shellQuote
	SinkPatternForTest = sinkPattern
)

// MapSinkForTest maps what a sink file holds.
func MapSinkForTest(path string) (usage.Usage, error) { return mapUsage(readStatus(path)) }

// CleanupForTest removes the transcripts a sink file reports.
func CleanupForTest(sink, dir, projects string) {
	removeTranscripts(readStatus(sink).transcripts, dir, projects)
}
