// Package codex reads subscription quotas through the user's Codex app-server.
package codex

import (
	"context"
	"flag"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const callTimeout = 10 * time.Second
const shutdownGrace = 3 * time.Second

// Provider runs one app-server probe per fetch, using Codex's own login.
type Provider struct {
	binary        string
	discovery     discovery
	callTimeout   time.Duration
	shutdownGrace time.Duration
}

// New creates an independently configured Codex provider.
func New() *Provider {
	return &Provider{discovery: systemDiscovery(), callTimeout: callTimeout, shutdownGrace: shutdownGrace}
}

// ID is the stable provider identifier.
func (*Provider) ID() string { return "codex" }

// DisplayName is the terminal display name.
func (*Provider) DisplayName() string { return "Codex" }

// AcceptsSecret is false: only Codex itself handles credentials.
func (*Provider) AcceptsSecret() bool { return false }

// BindFlags adds the optional absolute native binary path.
func (p *Provider) BindFlags(fs *flag.FlagSet) {
	fs.StringVar(&p.binary, "codex-bin", "", "Absolute path to the native Codex binary")
}

// Fetch discovers Codex, probes it once and maps the quota response.
func (p *Provider) Fetch(ctx context.Context, _ string) (usage.Usage, error) {
	binary, err := p.discovery.discover(p.binary)
	if err != nil {
		return usage.Usage{}, err
	}
	data, err := p.probe(ctx, binary)
	if err != nil {
		return usage.Usage{}, err
	}
	return mapUsage(data)
}

func unavailable(message string) *usage.Error {
	return &usage.Error{Code: usage.CodeUnavailable, Message: message}
}
