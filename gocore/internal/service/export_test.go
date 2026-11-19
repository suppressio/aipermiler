package service

import (
	"context"
	"net"
)

// Hooks for the external tests (package service_test).

// NewHTTPServer exposes the server configuration.
var NewHTTPServer = newHTTPServer

// WithListen replaces the loopback listener, to simulate a listener that dies.
func WithListen(opts Options, listen func(context.Context) (net.Listener, error)) Options {
	opts.listen = listen
	return opts
}
