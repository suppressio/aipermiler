package claude

import (
	"context"
	"os"
	"time"
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
	return p.probe(ctx, "claude", dir, "statusline-sink", nil)
}

var (
	NormalizeForTest       = normalize
	ChildEnvForTest        = childEnv
	ParseAuthStatusForTest = parseAuthStatus
	ClaudeArgsForTest      = claudeArgs
)

// TestPanelTimeout is TestTiming's wait for the panel.
var TestPanelTimeout = TestTiming.panelTimeout

// RealProbeForTest runs the real PTY and auth pre-check in dir, with extra
// environment for the child and TestTiming.
func RealProbeForTest(ctx context.Context, bin, dir string, extraEnv []string) error {
	p := systemProber()
	p.timing = TestTiming
	p.environ = func() []string { return append(os.Environ(), extraEnv...) }
	return p.probe(ctx, bin, dir, "true", nil)
}
