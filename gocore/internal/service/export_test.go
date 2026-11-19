package service

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Hooks for the external tests (package service_test).

// NewHTTPServer exposes the server configuration.
var NewHTTPServer = newHTTPServer

// Inflight exposes the request counter a stop waits on.
type Inflight = inflight

// Wrap and Wait expose its methods.
func (f *Inflight) Wrap(h http.Handler) http.Handler { return f.wrap(h) }
func (f *Inflight) Wait(d time.Duration) bool        { return f.wait(d) }

// RunWithListen is Run with a listener the test makes, to simulate one that dies.
func RunWithListen(ctx context.Context, opts Options, listen func(context.Context) (net.Listener, error)) error {
	return run(ctx, opts, listen)
}
