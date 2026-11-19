package service

import (
	"context"
	"net"
)

// Hooks for the external tests (package service_test).

// NewHTTPServer exposes the server configuration.
var NewHTTPServer = newHTTPServer

// RunWithListen is Run with a listener the test makes, to simulate one that dies.
func RunWithListen(ctx context.Context, opts Options, listen func(context.Context) (net.Listener, error)) error {
	return run(ctx, opts, listen)
}
