package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/refresh/policy"
	"github.com/suppressio/aipermiler/gocore/internal/service"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contract", "v1", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// lastGood is the fixture's usage as the loop would have committed it.
func lastGood(t *testing.T, name string) *store.LastGood {
	t.Helper()
	doc, err := usage.ParseDocument(fixture(t, name))
	if err != nil {
		t.Fatal(err)
	}
	lg := store.NewLastGood(doc.Usage, doc.FetchedAt)
	return &lg
}

// fakeLoop stands for refresh.Loop: errors per account, and a Refresh that
// records the modes it got and runs hook.
type fakeLoop struct {
	mu     sync.Mutex
	errs   map[string]error
	modes  []policy.Mode
	hook   func(ctx context.Context) error
	called chan struct{}
}

func (l *fakeLoop) Run(ctx context.Context) error { <-ctx.Done(); return nil }

func (l *fakeLoop) LastError(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.errs[id]
}

func (l *fakeLoop) Refresh(ctx context.Context, mode policy.Mode) error {
	l.mu.Lock()
	l.modes = append(l.modes, mode)
	hook := l.hook
	l.mu.Unlock()
	if l.called != nil {
		l.called <- struct{}{}
	}
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

type apiFixture struct {
	st   *store.Store
	loop *fakeLoop
	raw  service.RawFunc
}

func newAPI(t *testing.T, accounts ...store.Account) *apiFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error { s.Accounts = accounts; return nil }); err != nil {
		t.Fatal(err)
	}
	return &apiFixture{st: st, loop: &fakeLoop{errs: map[string]error{}}}
}

func (a *apiFixture) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://"+testHost+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testToken)
	h := service.NewHandler(service.HandlerConfig{
		Token: testToken, Host: testHost, Version: "1.2.3", ContractVersion: 1,
		Store: a.st, Loop: a.loop, Raw: a.raw,
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func fake(id string, lg *store.LastGood) store.Account {
	return store.Account{ID: id, Provider: "fake", Label: id, Enabled: true, LastGood: lg}
}

// The usage endpoint serves the documents of `usage --json`, byte for byte:
// the loop's last good read, stale while its last read failed.
func TestAccountUsageServesTheCLIDocuments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		lg      string // fixture whose usage is the last good read, "" for none
		err     error
		fixture string
		status  int
	}{
		{"fresh", "usage-ok", nil, "usage-ok", http.StatusOK},
		{"count window", "usage-count-window", nil, "usage-count-window", http.StatusOK},
		{"stale after a failed read", "usage-ok", &usage.Error{Code: usage.CodeUnavailable, Message: "Provider is temporarily unavailable"}, "usage-stale", http.StatusOK},
		{"no good read, failed", "", &usage.Error{Code: usage.CodeSignedOut, Message: "Sign in required"}, "error-signed-out", http.StatusBadGateway},
		{"no good read, wrapped error", "", errors.New("Provider is temporarily unavailable"), "error-unavailable", http.StatusBadGateway},
		{"not read yet", "", nil, "error-not-ready", http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var lg *store.LastGood
			if c.lg != "" {
				lg = lastGood(t, c.lg)
			}
			api := newAPI(t, fake("a1", lg))
			if c.err != nil {
				api.loop.errs["a1"] = c.err
			}
			w := api.do(t, http.MethodGet, "/api/v1/accounts/a1/usage", "")
			if w.Code != c.status || w.Body.String() != string(fixture(t, c.fixture)) {
				t.Errorf("status %d body %s; want %d %s", w.Code, w.Body, c.status, fixture(t, c.fixture))
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
		})
	}
}

// A child that outlived its deadline is a timeout, as a CLI deadline is.
func TestAccountUsageChildTimeoutIsATimeout(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fake("a1", lastGood(t, "usage-ok")))
	api.loop.errs["a1"] = fmt.Errorf("%w (2m15s)", refresh.ErrChildTimeout)
	var doc struct {
		Stale       bool            `json:"stale"`
		StaleReason usage.StaleInfo `json:"staleReason"`
	}
	w := api.do(t, http.MethodGet, "/api/v1/accounts/a1/usage", "")
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Stale || doc.StaleReason.Code != usage.CodeTimeout {
		t.Errorf("body = %s", w.Body)
	}
}

func TestAccountUsageUnknownOrDisabled(t *testing.T) {
	t.Parallel()
	off := fake("off", lastGood(t, "usage-ok"))
	off.Enabled = false
	api := newAPI(t, off)
	for _, id := range []string{"nope", "off"} {
		w := api.do(t, http.MethodGet, "/api/v1/accounts/"+id+"/usage", "")
		var doc struct {
			SchemaVersion int         `json:"schemaVersion"`
			Error         usage.Error `json:"error"`
		}
		if w.Code != http.StatusNotFound || json.Unmarshal(w.Body.Bytes(), &doc) != nil || doc.SchemaVersion != 1 {
			t.Fatalf("%s: status %d body %s", id, w.Code, w.Body)
		}
		if !strings.Contains(w.Body.String(), `"code":"usage"`) || strings.Contains(w.Body.String(), `"provider"`) {
			t.Errorf("%s: body %s; want a usage error without provider", id, w.Body)
		}
	}
}

// GET /api/v1/usage lists every enabled account, in the store's order.
func TestUsageListsTheEnabledAccounts(t *testing.T) {
	t.Parallel()
	off := fake("off", nil)
	off.Enabled = false
	api := newAPI(t, fake("a1", lastGood(t, "usage-ok")), off, fake("a2", nil))
	api.loop.errs["a2"] = &usage.Error{Code: usage.CodeSignedOut, Message: "Sign in required"}
	w := api.do(t, http.MethodGet, "/api/v1/usage", "")
	want := `{"schemaVersion":1,"accounts":[` +
		`{"id":"a1","document":` + strings.TrimSpace(string(fixture(t, "usage-ok"))) + `},` +
		`{"id":"a2","document":` + strings.TrimSpace(string(fixture(t, "error-signed-out"))) + `}]}` + "\n"
	if w.Code != http.StatusOK || w.Body.String() != want {
		t.Errorf("status %d body\n%s\nwant\n%s", w.Code, w.Body, want)
	}
}

// The service's own fixtures: /health and the account list (contract/v1/service).
func TestServiceFixtures(t *testing.T) {
	t.Parallel()
	health := serve(newRequest(http.MethodGet, "/api/v1/health"))
	want, err := os.ReadFile(filepath.Join("..", "..", "contract", "v1", "service", "health.json"))
	if err != nil {
		t.Fatal(err)
	}
	if health.Body.String() != string(want) {
		t.Errorf("health = %s; want %s", health.Body, want)
	}
	api := newAPI(t, fake("a1", lastGood(t, "usage-ok")))
	list := api.do(t, http.MethodGet, "/api/v1/usage", "")
	want, err = os.ReadFile(filepath.Join("..", "..", "contract", "v1", "service", "usage-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	if list.Body.String() != string(want) {
		t.Errorf("list = %s; want %s", list.Body, want)
	}
}

// POST /api/v1/refresh hands the mode to the loop (which coalesces runs) and
// answers with the documents read after it.
func TestRefreshRunsTheLoopAndAnswersWithTheDocuments(t *testing.T) {
	t.Parallel()
	for body, mode := range map[string]policy.Mode{
		"":                        policy.Manual,
		`{}`:                      policy.Manual,
		`{"mode":"scheduled"}`:    policy.Scheduled,
		`{"mode":"manual"}`:       policy.Manual,
		`{"mode":"forced"}`:       policy.Forced,
		`{"mode":"forced","x":1}`: policy.Forced,
	} {
		api := newAPI(t, fake("a1", nil))
		api.loop.hook = func(context.Context) error {
			// The read the refresh waited for: the answer must show it.
			return api.st.Update(func(s *store.State) error {
				s.Accounts[0].LastGood = lastGood(t, "usage-ok")
				return nil
			})
		}
		w := api.do(t, http.MethodPost, "/api/v1/refresh", body)
		want := `{"schemaVersion":1,"accounts":[{"id":"a1","document":` + strings.TrimSpace(string(fixture(t, "usage-ok"))) + `}]}` + "\n"
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("%q: status %d body %s", body, w.Code, w.Body)
		}
		if len(api.loop.modes) != 1 || api.loop.modes[0] != mode {
			t.Errorf("%q: modes = %v; want [%s]", body, api.loop.modes, mode)
		}
	}
}

func TestRefreshRejectsABadRequest(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"mode":"now"}`, `{"mode":1}`, `not json`, `{"mode":"manual"}{}`, `{"mode":"` + strings.Repeat("x", 8<<10) + `"}`} {
		api := newAPI(t, fake("a1", nil))
		w := api.do(t, http.MethodPost, "/api/v1/refresh", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"usage"`) {
			t.Errorf("%.20q: status %d body %s", body, w.Code, w.Body)
		}
		if len(api.loop.modes) != 0 {
			t.Errorf("%.20q: the loop ran", body)
		}
	}
}

// A refresh cut by the stop (ErrStopped, or the request's context) answers 503.
func TestRefreshCutByTheStop(t *testing.T) {
	t.Parallel()
	for name, hook := range map[string]func(context.Context) error{
		"loop stopped":    func(context.Context) error { return refresh.ErrStopped },
		"request context": func(context.Context) error { return context.Canceled },
	} {
		api := newAPI(t, fake("a1", nil))
		api.loop.hook = hook
		w := api.do(t, http.MethodPost, "/api/v1/refresh", "")
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"code":"unavailable"`) {
			t.Errorf("%s: status %d body %s", name, w.Code, w.Body)
		}
	}
}

// The refresh waits on the request's context: a stop (which cancels it) ends
// the wait instead of holding the connection open.
func TestRefreshHonoursTheRequestContext(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fake("a1", nil))
	api.loop.called = make(chan struct{}, 1)
	api.loop.hook = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://"+testHost+"/api/v1/refresh", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	h := service.NewHandler(service.HandlerConfig{Token: testToken, Host: testHost, Store: api.st, Loop: api.loop})
	done := make(chan struct{})
	go func() { h.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	<-api.loop.called
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh ignored the request context")
	}
}

// The raw endpoint passes the CLI's document through and maps its error code.
func TestRawEndpoint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		doc    string
		code   usage.Code
		err    error
		status int
		want   string
	}{
		{"raw document", "raw", "", nil, http.StatusOK, ""},
		{"provider error", "error-signed-out", usage.CodeSignedOut, nil, http.StatusBadGateway, ""},
		{"not supported", "error-usage", usage.CodeUsage, nil, http.StatusBadRequest, ""},
		{"child hung", "", "", fmt.Errorf("%w (40s)", refresh.ErrChildTimeout), http.StatusBadGateway, `"code":"timeout"`},
		{"bad output", "", "", fmt.Errorf("%w: raw output is not JSON", usage.ErrBadDocument), http.StatusBadGateway, `"code":"unavailable"`},
		{"cut by the stop", "", "", context.Canceled, http.StatusServiceUnavailable, `"code":"unavailable"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			api := newAPI(t, store.Account{ID: "g1", Provider: "copilot", Enabled: true})
			var got store.Account
			api.raw = func(_ context.Context, account store.Account) ([]byte, usage.Code, error) {
				got = account
				if c.doc == "" {
					return nil, "", c.err
				}
				return fixture(t, c.doc), c.code, nil
			}
			w := api.do(t, http.MethodGet, "/api/v1/accounts/g1/raw", "")
			if w.Code != c.status {
				t.Errorf("status = %d; want %d (%s)", w.Code, c.status, w.Body)
			}
			if c.doc != "" && w.Body.String() != string(fixture(t, c.doc)) {
				t.Errorf("body = %s; want the CLI's document", w.Body)
			}
			if c.want != "" && !strings.Contains(w.Body.String(), c.want) {
				t.Errorf("body = %s; want %s", w.Body, c.want)
			}
			if got.ID != "g1" {
				t.Errorf("raw read account %q", got.ID)
			}
		})
	}
	api := newAPI(t)
	api.raw = func(context.Context, store.Account) ([]byte, usage.Code, error) {
		t.Error("raw read an unknown account")
		return nil, "", nil
	}
	if w := api.do(t, http.MethodGet, "/api/v1/accounts/nope/raw", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown account: status %d", w.Code)
	}
}

// Run serves the account endpoints from its store and loop, and a stop ends a
// refresh that is waiting (#248: endpoints honour the request context).
func TestRunServesTheAccountsAndAStopEndsARefresh(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Accounts = []store.Account{fake("a1", lastGood(t, "usage-ok"))}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loop := &fakeLoop{errs: map[string]error{}, called: make(chan struct{}, 1)}
	loop.hook = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	r := startWith(t, service.Options{Dir: dir, Loop: func(*store.Store) service.Loop { return loop }}, nil)
	do := func(method, path string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(context.Background(), method, r.url(path), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+r.info.Token)
		return http.DefaultClient.Do(req)
	}
	resp, err := do(http.MethodGet, "/api/v1/accounts/a1/usage")
	if err != nil {
		t.Fatal(err)
	}
	body := new(strings.Builder)
	_, _ = io.Copy(body, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || body.String() != string(fixture(t, "usage-ok")) {
		t.Errorf("usage: %d %s", resp.StatusCode, body)
	}
	refreshed := make(chan struct{})
	go func() {
		if resp, err := do(http.MethodPost, "/api/v1/refresh"); err == nil {
			_ = resp.Body.Close()
		}
		close(refreshed)
	}()
	<-loop.called
	if err := r.stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-refreshed:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop left the refresh waiting")
	}
}
