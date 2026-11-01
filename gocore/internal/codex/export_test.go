package codex

import "time"

// Discovery exposes only the discovery seam to external tests.
type Discovery = discovery

// NewForTest injects discovery and shorter lifecycle deadlines.
func NewForTest(d Discovery, timeout, grace time.Duration) *Provider {
	if d.ReadHeader == nil {
		d.ReadHeader = readHeader
	}
	return &Provider{discovery: d, callTimeout: timeout, shutdownGrace: grace}
}

// DiscoverForTest exercises discovery without spawning a process.
func DiscoverForTest(d Discovery, explicit string) (string, error) { return d.discover(explicit) }

// MapForTest exercises the pure response mapper.
var MapForTest = mapUsage

// SplitPathListForTest exercises PATH splitting for a simulated OS.
var SplitPathListForTest = splitPathList
