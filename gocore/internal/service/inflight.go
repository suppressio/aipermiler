package service

import (
	"net/http"
	"sync/atomic"
	"time"
)

// inflight counts the requests being served. A forced close does not wait for
// handlers, so a stop waits on this before it releases the single-instance lock:
// a handler that ignored the cancellation must not still be writing while the
// next service starts.
type inflight struct {
	n atomic.Int64
}

func (f *inflight) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.n.Add(1)
		defer f.n.Add(-1)
		h.ServeHTTP(w, r)
	})
}

// wait reports whether every handler returned within d. Polling, not a
// WaitGroup: a request accepted just before the close may still be counted in
// while we wait, which WaitGroup forbids.
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
