package service

import (
	"net/http"
	"sync/atomic"
	"time"
)

// inflight counts the requests being served. A forced close does not wait for
// handlers, so a stop closes the gate and waits on the count before it releases
// the single-instance lock: a handler that ignored the cancellation must not
// still be writing while the next service starts.
type inflight struct {
	n      atomic.Int64
	closed atomic.Bool
}

// wrap counts a request before it looks at the gate. With that order a request
// either is counted before close (and wait sees it) or sees the gate closed and
// does no work: none slips in after wait has found the count at zero.
func (f *inflight) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.n.Add(1)
		defer f.n.Add(-1)
		if f.closed.Load() {
			http.Error(w, "service stopping", http.StatusServiceUnavailable)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// close shuts the gate: requests that have not started yet get 503.
func (f *inflight) close() { f.closed.Store(true) }

// wait reports whether every started handler returned within d. It polls: it
// runs once per stop, for at most handlerGrace, and needs no extra goroutine
// or channel bookkeeping.
func (f *inflight) wait(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for f.n.Load() > 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}
