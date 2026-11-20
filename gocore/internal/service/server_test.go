package service_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/service"
)

const (
	testToken = "0123456789abcdef"
	testHost  = "127.0.0.1:4242"
)

func newRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, "http://"+testHost+path, nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	return r
}

func serve(r *http.Request) *httptest.ResponseRecorder {
	h := service.NewHandler(service.HandlerConfig{Token: testToken, Host: testHost, Version: "1.2.3", ContractVersion: 1})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHealth(t *testing.T) {
	t.Parallel()
	w := serve(newRequest(http.MethodGet, "/api/v1/health"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if got, want := strings.TrimSpace(w.Body.String()), `{"schemaVersion":1,"version":"1.2.3","contractVersion":1}`; got != want {
		t.Errorf("body = %s; want %s", got, want)
	}
}

func TestRejections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*http.Request)
		method string
		path   string
		want   int
	}{
		{"no token", func(r *http.Request) { r.Header.Del("Authorization") }, http.MethodGet, "/api/v1/health", http.StatusUnauthorized},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.MethodGet, "/api/v1/health", http.StatusUnauthorized},
		{"token of a different length", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testToken+"x") }, http.MethodGet, "/api/v1/health", http.StatusUnauthorized},
		{"wrong scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic "+testToken) }, http.MethodGet, "/api/v1/health", http.StatusUnauthorized},
		{"scheme alone", func(r *http.Request) { r.Header.Set("Authorization", "Bearer") }, http.MethodGet, "/api/v1/health", http.StatusUnauthorized},
		{"scheme and spaces only", func(r *http.Request) { r.Header.Set("Authorization", "Bearer   ") }, http.MethodGet, "/api/v1/health", http.StatusUnauthorized},
		{"tab instead of space", func(r *http.Request) { r.Header.Set("Authorization", "Bearer\t"+testToken) }, http.MethodGet, "/api/v1/health", http.StatusUnauthorized},
		{"scheme with a suffix", func(r *http.Request) { r.Header.Set("Authorization", "Bearerx "+testToken) }, http.MethodGet, "/api/v1/health", http.StatusUnauthorized},
		{"token without scheme", func(r *http.Request) { r.Header.Set("Authorization", testToken) }, http.MethodGet, "/api/v1/health", http.StatusUnauthorized},
		{"host is localhost", func(r *http.Request) { r.Host = "localhost:4242" }, http.MethodGet, "/api/v1/health", http.StatusForbidden},
		{"host is a rebinding name", func(r *http.Request) { r.Host = "evil.example:4242" }, http.MethodGet, "/api/v1/health", http.StatusForbidden},
		{"host with another port", func(r *http.Request) { r.Host = "127.0.0.1:9" }, http.MethodGet, "/api/v1/health", http.StatusForbidden},
		{"origin header", func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, http.MethodGet, "/api/v1/health", http.StatusForbidden},
		{"empty origin header", func(r *http.Request) { r.Header["Origin"] = []string{""} }, http.MethodGet, "/api/v1/health", http.StatusForbidden},
		{"origin null", func(r *http.Request) { r.Header.Set("Origin", "null") }, http.MethodGet, "/api/v1/health", http.StatusForbidden},
		{"unknown path", func(*http.Request) {}, http.MethodGet, "/api/v1/nope", http.StatusNotFound},
		{"wrong method", func(*http.Request) {}, http.MethodPost, "/api/v1/health", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRequest(tt.method, tt.path)
			tt.mutate(r)
			w := serve(r)
			if w.Code != tt.want {
				t.Fatalf("status = %d; want %d", w.Code, tt.want)
			}
			if strings.Contains(w.Body.String(), testToken) {
				t.Error("the response leaks the token")
			}
		})
	}
}

func TestHostIsCheckedBeforeToken(t *testing.T) {
	t.Parallel()
	r := newRequest(http.MethodGet, "/api/v1/health")
	r.Host = "evil.example:4242"
	r.Header.Del("Authorization")
	if w := serve(r); w.Code != http.StatusForbidden {
		t.Fatalf("status = %d; a bad Host must be rejected before authentication (403)", w.Code)
	}
}

// RFC 7235: the scheme is case-insensitive and may be followed by several spaces.
func TestBearerSchemeIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"bearer " + testToken, "BEARER " + testToken, "Bearer  " + testToken} {
		r := newRequest(http.MethodGet, "/api/v1/health")
		r.Header.Set("Authorization", value)
		if w := serve(r); w.Code != http.StatusOK {
			t.Errorf("%q: status = %d; want 200", value[:8], w.Code)
		}
	}
}

func TestShutdownStartsTheStopAfterAnswering(t *testing.T) {
	t.Parallel()
	calls := 0
	w := httptest.NewRecorder()
	h := service.NewHandler(service.HandlerConfig{Token: testToken, Host: testHost, Shutdown: func() {
		calls++
		// The stop cancels the request: the reply must already be out.
		if !w.Flushed || w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), "stopping") {
			t.Error("Shutdown ran before the 202 reply was written and flushed")
		}
	}})
	h.ServeHTTP(w, newRequest(http.MethodPost, "/api/v1/shutdown"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d; want 202", w.Code)
	}
	if got, want := strings.TrimSpace(w.Body.String()), `{"schemaVersion":1,"status":"stopping"}`; got != want {
		t.Errorf("body = %s; want %s", got, want)
	}
	if calls != 1 {
		t.Errorf("shutdown called %d times; want 1", calls)
	}
}

func TestShutdownIsAuthenticatedAndPostOnly(t *testing.T) {
	t.Parallel()
	calls := 0
	h := service.NewHandler(service.HandlerConfig{Token: testToken, Host: testHost, Shutdown: func() { calls++ }})
	for _, tt := range []struct {
		name   string
		method string
		mutate func(*http.Request)
		want   int
	}{
		{"no token", http.MethodPost, func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{"wrong token", http.MethodPost, func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.StatusUnauthorized},
		{"origin", http.MethodPost, func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, http.StatusForbidden},
		{"foreign host", http.MethodPost, func(r *http.Request) { r.Host = "evil.example:4242" }, http.StatusForbidden},
		{"GET", http.MethodGet, func(*http.Request) {}, http.StatusMethodNotAllowed},
	} {
		r := newRequest(tt.method, "/api/v1/shutdown")
		tt.mutate(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Errorf("%s: status = %d; want %d", tt.name, w.Code, tt.want)
		}
	}
	if calls != 0 {
		t.Errorf("a rejected request started the stop %d times", calls)
	}
}
