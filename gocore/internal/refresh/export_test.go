package refresh

import "github.com/suppressio/aipermiler/gocore/internal/refresh/policy"

// Busy reports a run in progress (tests wait for the loop to go idle).
func (l *Loop) Busy() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.busy
}

// Pending is the mode of the requested run, "" when none.
func (l *Loop) Pending() policy.Mode {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pending
}
