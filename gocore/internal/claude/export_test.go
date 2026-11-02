package claude

// Discovery exposes only the discovery seam to external tests.
type Discovery = discovery

// DiscoverForTest exercises discovery without spawning a process.
func DiscoverForTest(d Discovery, explicit string) (string, error) { return d.discover(explicit) }

// CandidatesForTest lists every candidate found, in order.
func CandidatesForTest(d Discovery) []string { return d.candidates() }
