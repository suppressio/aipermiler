package copilot

import (
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/github"
	"github.com/suppressio/aipermiler/gocore/internal/secrets"
)

// NewForTest points the provider at a fake GitHub (both base URLs), a fixed clock and a
// loopback on any free port.
func NewForTest(store secrets.Store, base string, now time.Time) *Provider {
	p := New(store)
	p.endpoints = &github.Endpoints{API: base, OAuth: base + "/login/oauth"}
	p.now = func() time.Time { return now }
	p.listenAddr = "127.0.0.1:0"
	return p
}

// APIForTest is the API base the provider would use for its --host.
func (p *Provider) APIForTest() string { return p.apiEndpoints().API }
