package claude

import (
	"bytes"
	"context"
	"flag"
	"os"
	"runtime"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// countRuns counts the status line runs in the sink: one line each, written whole.
// Cheaper than readStatus, for the prompt wait that asks every tick.
func countRuns(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return bytes.Count(data, []byte{'\n'})
}

// statusWait is how long the probe waits, after the panel closed, for a status line
// run with rate_limits (measured well under a second on #183).
const statusWait = 3 * time.Second

// Provider runs one `claude` probe per fetch, using Claude Code's own sign-in.
type Provider struct {
	binary     string
	discovery  discovery
	prober     prober
	self       func() (string, error)
	base       func() (string, error)
	statusWait time.Duration
}

// New creates an independently configured Claude provider.
func New() *Provider {
	return &Provider{discovery: systemDiscovery(), prober: systemProber(), self: os.Executable, base: cacheBase, statusWait: statusWait}
}

// ID is the stable provider identifier.
func (*Provider) ID() string { return "claude" }

// DisplayName is the terminal display name.
func (*Provider) DisplayName() string { return "Claude" }

// AcceptsSecret is false: only Claude Code itself handles credentials.
func (*Provider) AcceptsSecret() bool { return false }

// BindFlags adds the optional absolute native binary path.
func (p *Provider) BindFlags(fs *flag.FlagSet) {
	fs.StringVar(&p.binary, "claude-bin", "", "Absolute path to the native Claude Code binary")
}

// Fetch discovers Claude Code, runs one probe with this binary as its status line,
// maps the rate_limits it received and removes the probe's own transcript.
func (p *Provider) Fetch(ctx context.Context, _ string) (usage.Usage, error) {
	bin, err := p.discovery.discover(p.binary)
	if err != nil {
		return usage.Usage{}, err
	}
	base, err := p.base()
	if err != nil {
		return usage.Usage{}, err
	}
	dir, err := probeDir(base)
	if err != nil {
		return usage.Usage{}, err
	}
	self, err := p.self()
	if err != nil {
		return usage.Usage{}, unavailable("locate the core binary: " + err.Error())
	}
	removeStaleSinks(base, time.Now())
	sink, err := os.CreateTemp(base, sinkPattern)
	if err != nil {
		return usage.Usage{}, unavailable("create the status line file: " + err.Error())
	}
	path := sink.Name()
	_ = sink.Close()
	defer func() { _ = os.Remove(path) }()
	command, err := statusCommand(self, path, runtime.GOOS, shortPath)
	if err != nil {
		return usage.Usage{}, err
	}
	// The deadline came while rate_limits were awaited: their absence then says
	// nothing about the format (#266).
	cut := false
	status, err := p.prober.probe(ctx, bin, dir, command, func() int { return countRuns(path) }, func(ctx context.Context) error {
		cut = waitForRateLimits(ctx.Done(), path, p.statusWait)
		return nil
	})
	read := readStatus(path)
	removeTranscripts(read.transcripts, dir, status.projects)
	if err != nil {
		return usage.Usage{}, err
	}
	if cut && read.latest == nil && (!status.credits || read.runs == 0) {
		return usage.Usage{}, &usage.Error{Code: usage.CodeTimeout, Message: "claude probe timed out before the status line carried rate_limits"}
	}
	if read.latest == nil && read.runs <= status.promptRuns && !status.credits {
		// Claude ran the status line at the prompt but not after /usage (it runs it
		// again as the panel closes, with rate_limits): no data came, so nothing says
		// the format changed (#266).
		return usage.Usage{}, notReady("claude ran no status line after /usage")
	}
	if status.credits && read.runs > 0 && read.latest == nil {
		return mapCredits(status.panel, time.Now())
	}
	return mapUsage(read)
}
