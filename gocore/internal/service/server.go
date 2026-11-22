package service

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/suppressio/aipermiler/gocore/internal/store"
)

// HandlerConfig is what the HTTP handler needs to know about the running service.
type HandlerConfig struct {
	Token           string // bearer token required on every request
	Host            string // the only accepted Host header, "127.0.0.1:<port>"
	Version         string
	ContractVersion int
	// Shutdown starts the clean stop (POST /api/v1/shutdown); nil does nothing.
	Shutdown func()
	// Store, Loop and Raw serve the account endpoints (#248); without a Store
	// they are not registered. A nil Loop never reads, a nil Raw answers an error.
	Store *store.Store
	Loop  Loop
	Raw   RawFunc
}

// NewHandler builds the API. Every request passes three gates before routing:
// no Origin header, the exact loopback Host (DNS rebinding), then the bearer
// token compared in constant time. The token is the real protection: browsers
// send no Origin on simple cross-origin GETs, so the Origin gate only narrows
// what a page can try and must never be relied on in place of the token.
func NewHandler(cfg HandlerConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			SchemaVersion   int    `json:"schemaVersion"`
			Version         string `json:"version"`
			ContractVersion int    `json:"contractVersion"`
		}{SchemaVersion, cfg.Version, cfg.ContractVersion})
	})
	mux.HandleFunc("POST /api/v1/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(struct {
			SchemaVersion int    `json:"schemaVersion"`
			Status        string `json:"status"`
		}{SchemaVersion, "stopping"})
		// Answer first: the stop cancels the request contexts and closes the
		// connections once it drains, so the caller must already have its reply.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if cfg.Shutdown != nil {
			cfg.Shutdown()
		}
	})
	if cfg.Store != nil {
		loop := cfg.Loop
		if loop == nil {
			loop = noLoop{}
		}
		rawSlot := make(chan struct{}, 1)
		rawSlot <- struct{}{}
		accountsAPI{store: cfg.Store, loop: loop, raw: cfg.Raw, rawSlot: rawSlot}.register(mux)
	}
	want := sha256.Sum256([]byte(cfg.Token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, hasOrigin := r.Header["Origin"]; hasOrigin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Host != cfg.Host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// RFC 7235: the scheme is case-insensitive, then 1*SP; the token is not.
		scheme, bearer, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		ok = ok && strings.EqualFold(scheme, "Bearer")
		got := sha256.Sum256([]byte(strings.TrimLeft(bearer, " ")))
		if !ok || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
