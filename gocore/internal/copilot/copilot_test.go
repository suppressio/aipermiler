package copilot_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/copilot"
	"github.com/suppressio/aipermiler/gocore/internal/secrets"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const token = "synthetic-token-value"

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

type reply struct {
	status int
	body   string
}

// fakeGitHub answers by path (query ignored) and records the paths it was asked for.
type fakeGitHub struct {
	*httptest.Server
	mu     sync.Mutex
	routes map[string]reply
	calls  []string
}

func newFakeGitHub(t *testing.T, routes map[string]reply) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{routes: routes}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.URL.Path)
		answer, ok := f.routes[r.URL.Path]
		f.mu.Unlock()
		if r.URL.Path != "/login/oauth/access_token" && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
			return
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		if answer.status != 0 {
			w.WriteHeader(answer.status)
		}
		_, _ = io.WriteString(w, answer.body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHub) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func provider(t *testing.T, base string, store secrets.Store, flags ...string) *copilot.Provider {
	t.Helper()
	p := copilot.NewForTest(store, base, now)
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	p.BindFlags(fs)
	p.BindLoginFlags(fs)
	if err := fs.Parse(append([]string{"--account", "copilot-1"}, flags...)); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	return p
}

func connectedStore(t *testing.T) *secrets.Memory {
	t.Helper()
	store := &secrets.Memory{}
	if err := store.Set(secrets.CopilotKey("copilot-1"), token); err != nil {
		t.Fatal(err)
	}
	return store
}

type windowRow struct {
	ID    string
	Unit  usage.Unit
	Used  float64
	Total any
}

func rows(u usage.Usage) []windowRow {
	out := []windowRow{}
	for _, w := range u.QuotaWindows {
		var total any
		if w.Total != nil {
			total = *w.Total
		}
		out = append(out, windowRow{w.ID, w.Unit, w.Used, total})
	}
	return out
}

func equalJSON(t *testing.T, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("got  %s\nwant %s", g, w)
	}
}

func TestCompanySeatSnapshots(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want []windowRow
		plan string
	}{
		{"percentage windows, key order kept", `{"copilot_plan":"business","quota_reset_date":"2026-08-01T00:00:00Z","quota_snapshots":{"premium_interactions":{"percent_remaining":40},"chat":{"percent_remaining":90}}}`,
			[]windowRow{{"premium_interactions", usage.UnitPercentage, 60, nil}, {"chat", usage.UnitPercentage, 10, nil}}, "business"},
		{"reverse key order kept", `{"quota_snapshots":{"zeta":{"percent_remaining":50},"alpha":{"percent_remaining":25}}}`,
			[]windowRow{{"zeta", usage.UnitPercentage, 50, nil}, {"alpha", usage.UnitPercentage, 75, nil}}, ""},
		{"unlimited with credits: count without total", `{"copilot_plan":"individual","quota_snapshots":{"premium_interactions":{"unlimited":true,"has_quota":true,"percent_remaining":100,"credits_used":321,"entitlement":0}}}`,
			[]windowRow{{"premium_interactions", usage.UnitCount, 321, nil}}, "individual"},
		{"entitlement: used = entitlement − remaining", `{"quota_snapshots":{"premium_interactions":{"unlimited":false,"percent_remaining":70,"entitlement":1500,"quota_remaining":1050,"credits_used":450}}}`,
			[]windowRow{{"premium_interactions", usage.UnitCount, 450, 1500.0}}, ""},
		{"fractional credits rounded", `{"quota_snapshots":{"p":{"percent_remaining":99,"entitlement":10,"quota_remaining":9.6}}}`,
			[]windowRow{{"p", usage.UnitCount, 0.4, 10.0}}, ""},
		{"nothing to show is not a drift", `{"quota_snapshots":{"chat":{"unlimited":true,"has_quota":false,"percent_remaining":100,"credits_used":0},"completions":{"unlimited":true,"percent_remaining":100},"premium_interactions":{"unlimited":false,"percent_remaining":100,"entitlement":0}}}`,
			[]windowRow{}, ""},
		{"Free plan under token-based billing", `{"copilot_plan":"individual","quota_reset_date":"2026-11-01","quota_snapshots":{"chat":{"unlimited":false,"has_quota":true,"entitlement":200,"quota_remaining":200,"percent_remaining":100,"credits_used":0},"completions":{"unlimited":false,"has_quota":true,"entitlement":2000,"quota_remaining":2000,"percent_remaining":100,"credits_used":0},"premium_interactions":{"unlimited":false,"has_quota":false,"entitlement":0,"quota_remaining":0,"percent_remaining":0,"credits_used":0}}}`,
			[]windowRow{{"chat", usage.UnitCount, 0, 200.0}, {"completions", usage.UnitCount, 0, 2000.0}}, "individual"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gh := newFakeGitHub(t, map[string]reply{"/copilot_internal/user": {body: tt.body}})
			got, err := provider(t, gh.URL, connectedStore(t), "--scope", "organization").Fetch(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			equalJSON(t, rows(got), tt.want)
			plan := ""
			if got.PlanTier != nil {
				plan = *got.PlanTier
			}
			if plan != tt.plan {
				t.Errorf("plan = %q; want %q", plan, tt.plan)
			}
			for _, w := range got.QuotaWindows {
				if w.PeriodType != usage.PeriodBillingCycle || w.PeriodLength == nil || *w.PeriodLength != 1 || w.Label != "Copilot — "+w.ID {
					t.Errorf("window %s: %+v", w.ID, w)
				}
			}
		})
	}
}

func TestResets(t *testing.T) {
	t.Parallel()
	body := `{"quota_reset_date":"2026-11-01","quota_snapshots":{"a":{"percent_remaining":50},"b":{"percent_remaining":50,"quota_reset_at":1793491200}}}`
	gh := newFakeGitHub(t, map[string]reply{"/copilot_internal/user": {body: body}})
	got, err := provider(t, gh.URL, connectedStore(t), "--scope", "organization").Fetch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.SubscriptionRenewsAt == nil || !got.SubscriptionRenewsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("renews = %v", got.SubscriptionRenewsAt)
	}
	a, b := got.QuotaWindows[0], got.QuotaWindows[1]
	if a.ResetsAt == nil || !a.ResetsAt.Equal(*got.SubscriptionRenewsAt) {
		t.Errorf("fallback reset = %v", a.ResetsAt)
	}
	if b.ResetsAt == nil || b.ResetsAt.Unix() != 1793491200 {
		t.Errorf("own reset = %v", b.ResetsAt)
	}
}

func asUsageError(t *testing.T, err error) *usage.Error {
	t.Helper()
	var known *usage.Error
	if !errors.As(err, &known) {
		t.Fatalf("error %v (%T) is not a usage.Error", err, err)
	}
	return known
}

func TestInternalErrors(t *testing.T) {
	t.Parallel()
	t.Run("enterprise-managed seat is unavailable with its reason, not a drift", func(t *testing.T) {
		t.Parallel()
		gh := newFakeGitHub(t, map[string]reply{"/copilot_internal/user": {body: `{"access_type_sku":"enterprise_managed","copilot_plan":"individual","chat_enabled":true}`}})
		_, err := provider(t, gh.URL, connectedStore(t), "--scope", "organization").Fetch(context.Background(), "")
		e := asUsageError(t, err)
		if e.Code != usage.CodeUnavailable || e.Reason != copilot.ReasonEnterpriseManaged {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("no quota_snapshots: format drift with the shape only", func(t *testing.T) {
		t.Parallel()
		gh := newFakeGitHub(t, map[string]reply{"/copilot_internal/user": {body: `{"copilot_plan":"business","seats":4242,"note":"synthetic-value"}`}})
		_, err := provider(t, gh.URL, connectedStore(t), "--scope", "organization").Fetch(context.Background(), "")
		e := asUsageError(t, err)
		shape, _ := json.Marshal(e.Shape)
		if e.Code != usage.CodeFormatDrift || e.Endpoint != "copilot_internal/user" || strings.Contains(string(shape), "4242") || strings.Contains(string(shape), "synthetic-value") {
			t.Fatalf("%+v shape=%s", e, shape)
		}
	})
	t.Run("no snapshot recognized: drift", func(t *testing.T) {
		t.Parallel()
		gh := newFakeGitHub(t, map[string]reply{"/copilot_internal/user": {body: `{"quota_snapshots":{"chat":{"remaining":3}}}`}})
		_, err := provider(t, gh.URL, connectedStore(t), "--scope", "organization").Fetch(context.Background(), "")
		if e := asUsageError(t, err); e.Code != usage.CodeFormatDrift {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("a token GitHub rejects is signed out, redacted", func(t *testing.T) {
		t.Parallel()
		gh := newFakeGitHub(t, nil)
		store := &secrets.Memory{}
		_ = store.Set(secrets.CopilotKey("copilot-1"), "another-token")
		_, err := provider(t, gh.URL, store, "--scope", "organization").Fetch(context.Background(), "")
		e := asUsageError(t, err)
		if e.Code != usage.CodeSignedOut || e.Reason != copilot.ReasonTokenRejected || strings.Contains(e.Message, "another-token") {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("not connected and keyring unavailable", func(t *testing.T) {
		t.Parallel()
		gh := newFakeGitHub(t, nil)
		_, err := provider(t, gh.URL, &secrets.Memory{}).Fetch(context.Background(), "")
		if e := asUsageError(t, err); e.Code != usage.CodeSignedOut || e.Reason != copilot.ReasonNotConnected {
			t.Fatalf("%+v", e)
		}
		_, err = provider(t, gh.URL, &secrets.Memory{Err: errors.New("no secret service")}).Fetch(context.Background(), "")
		if e := asUsageError(t, err); e.Code != usage.CodeUnavailable || e.Reason != copilot.ReasonKeyringUnavailable {
			t.Fatalf("%+v", e)
		}
		if len(gh.paths()) != 0 {
			t.Errorf("network called without a token: %v", gh.paths())
		}
	})
}

const billingReport = `{"usageItems":[{"netAmount":1.25},{"netAmount":"0.75"},{"netAmount":null}]}`

func TestPersonalScope(t *testing.T) {
	t.Parallel()
	quotas := reply{body: `{"copilot_plan":"individual","quota_snapshots":{"chat":{"unlimited":false,"has_quota":true,"entitlement":200,"quota_remaining":150,"percent_remaining":75}}}`}
	empty := reply{body: `{"copilot_plan":"individual","quota_snapshots":{"premium_interactions":{"unlimited":false,"percent_remaining":100,"entitlement":0}}}`}
	user := reply{body: `{"login":"octo"}`}
	tests := []struct {
		name   string
		routes map[string]reply
		want   []windowRow
		paths  []string
		code   usage.Code
		reason string
	}{
		{"internal quotas first, billing skipped", map[string]reply{"/copilot_internal/user": quotas},
			[]windowRow{{"chat", usage.UnitCount, 50, 200.0}}, []string{"/copilot_internal/user"}, "", ""},
		{"no internal quotas: billing ai_credit with the manual quota", map[string]reply{"/copilot_internal/user": {body: `{"quota_snapshots":null}`}, "/user": user, "/users/octo/settings/billing/ai_credit/usage": {body: billingReport}},
			[]windowRow{{"ai_credits", usage.UnitCount, 200, 300.0}}, []string{"/copilot_internal/user", "/user", "/users/octo/settings/billing/ai_credit/usage"}, "", ""},
		{"ai_credit 404 → premium_request", map[string]reply{"/copilot_internal/user": {status: 401, body: `{}`}, "/user": user, "/users/octo/settings/billing/premium_request/usage": {body: billingReport}},
			[]windowRow{{"ai_credits", usage.UnitCount, 200, 300.0}}, []string{"/copilot_internal/user", "/user", "/users/octo/settings/billing/ai_credit/usage", "/users/octo/settings/billing/premium_request/usage"}, "", ""},
		{"400 Unable to get billing usage data → fallback", map[string]reply{"/copilot_internal/user": {status: 401, body: `{}`}, "/user": user, "/users/octo/settings/billing/ai_credit/usage": {status: 400, body: `{"message":"Unable to get billing usage data."}`}, "/users/octo/settings/billing/premium_request/usage": {body: billingReport}},
			[]windowRow{{"ai_credits", usage.UnitCount, 200, 300.0}}, nil, "", ""},
		{"another 400 does not fall back", map[string]reply{"/copilot_internal/user": {status: 401, body: `{}`}, "/user": user, "/users/octo/settings/billing/ai_credit/usage": {status: 400, body: `{"message":"Bad request"}`}},
			nil, []string{"/copilot_internal/user", "/user", "/users/octo/settings/billing/ai_credit/usage"}, usage.CodeUnavailable, ""},
		{"recognized but empty internal kept when no billing applies", map[string]reply{"/copilot_internal/user": empty, "/user": user},
			[]windowRow{}, nil, "", ""},
		{"neither applies: the internal reason", map[string]reply{"/copilot_internal/user": {body: `{"access_type_sku":"enterprise_managed"}`}, "/user": user},
			nil, nil, usage.CodeUnavailable, copilot.ReasonEnterpriseManaged},
		{"billing usageItems missing: drift", map[string]reply{"/copilot_internal/user": {status: 401, body: `{}`}, "/user": user, "/users/octo/settings/billing/ai_credit/usage": {body: `{"items":[]}`}},
			nil, nil, usage.CodeFormatDrift, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gh := newFakeGitHub(t, tt.routes)
			got, err := provider(t, gh.URL, connectedStore(t), "--manual-quota", "300").Fetch(context.Background(), "")
			if tt.code != "" {
				e := asUsageError(t, err)
				if e.Code != tt.code || e.Reason != tt.reason {
					t.Fatalf("%+v; want %s/%s", e, tt.code, tt.reason)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				equalJSON(t, rows(got), tt.want)
			}
			if tt.paths != nil {
				equalJSON(t, gh.paths(), tt.paths)
			}
		})
	}
}

func TestBillingWindow(t *testing.T) {
	t.Parallel()
	gh := newFakeGitHub(t, map[string]reply{"/copilot_internal/user": {status: 403, body: `{}`}, "/user": {body: `{"login":"octo"}`}, "/users/octo/settings/billing/ai_credit/usage": {body: billingReport}})
	got, err := provider(t, gh.URL, connectedStore(t)).Fetch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	w := got.QuotaWindows[0]
	reset := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if w.Label != "AI credits" || w.PeriodType != usage.PeriodBillingCycle || *w.PeriodLength != 1 || w.Total != nil || !w.ResetsAt.Equal(reset) || !got.SubscriptionRenewsAt.Equal(reset) || got.PlanTier != nil {
		t.Fatalf("%+v", got)
	}
	if !copilot.BillingReportResetsAt(time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC)).Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("December does not roll over to January")
	}
}

func TestValidateAndHost(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{}, {"--account", "a/b"}, {"--account", "a", "--host", "evil.com"}, {"--account", "a", "--scope", "team"}, {"--account", "a", "--manual-quota", "-1"}, {"--account", "a", "--manual-quota", "x"},
		{"--account", "a", "--manual-quota", "NaN"}, {"--account", "a", "--manual-quota", "+Inf"}, {"--account", "a", "--manual-quota", "inf"},
	} {
		p := copilot.New(&secrets.Memory{})
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		p.BindFlags(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		if p.Validate() == nil {
			t.Errorf("%v: Validate() accepted invalid flags", args)
		}
	}
	p := copilot.New(&secrets.Memory{})
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	p.BindFlags(fs)
	_ = fs.Parse([]string{"--account", "a", "--host", "https://API.Acme.ghe.com/"})
	if err := p.Validate(); err != nil || p.APIForTest() != "https://api.acme.ghe.com" {
		t.Fatalf("tenant host: %v %q", err, p.APIForTest())
	}
}

func TestTokens(t *testing.T) {
	t.Parallel()
	gh := newFakeGitHub(t, map[string]reply{"/user": {body: `{"login":"octo"}`}})
	store := &secrets.Memory{}
	p := provider(t, gh.URL, store)
	if _, err := p.SetToken(context.Background(), "wrong-token", true); asUsageError(t, err).Code != usage.CodeSignedOut {
		t.Fatalf("rejected token: %v", err)
	}
	if _, err := store.Get(secrets.CopilotKey("copilot-1")); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("a rejected token was stored")
	}
	username, err := p.SetToken(context.Background(), token+"\n", true)
	if err != nil || username != "octo" {
		t.Fatalf("SetToken = %q, %v", username, err)
	}
	if v, _ := store.Get(secrets.CopilotKey("copilot-1")); v != token {
		t.Fatalf("stored %q", v)
	}
	if u, err := p.SetToken(context.Background(), "unchecked-token", false); err != nil || u != "" || len(gh.paths()) != 2 {
		t.Fatalf("SetToken unverified = %q, %v, calls %v", u, err, gh.paths())
	}
	if v, _ := store.Get(secrets.CopilotKey("copilot-1")); v != "unchecked-token" {
		t.Fatalf("stored %q", v)
	}
	if err := p.DeleteToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(secrets.CopilotKey("copilot-1")); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("token still stored after delete")
	}
}

func TestRawResponse(t *testing.T) {
	t.Parallel()
	body := `{"z":1,"a":{"quota_snapshots":{}}}`
	gh := newFakeGitHub(t, map[string]reply{"/copilot_internal/user": {body: body}})
	endpoint, raw, err := provider(t, gh.URL, connectedStore(t)).RawResponse(context.Background())
	if err != nil || endpoint != copilot.InternalEndpointLabel || string(raw) != body {
		t.Fatalf("RawResponse = %q, %s, %v", endpoint, raw, err)
	}
}

func TestLogin(t *testing.T) {
	t.Parallel()
	var exchange map[string]string
	var mu sync.Mutex
	gh := newFakeGitHub(t, map[string]reply{"/user": {body: `{"login":"octo"}`}})
	gh.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/oauth/access_token" {
			mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&exchange)
			mu.Unlock()
			_, _ = io.WriteString(w, `{"access_token":"`+token+`"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"login":"octo"}`)
	})
	store := &secrets.Memory{}
	p := provider(t, gh.URL, store, "--client-id", "Iv1.synthetic", "--locale", "it")
	pages := make(chan string, 1)
	announce := func(authorize string) error {
		u, err := url.Parse(authorize)
		if err != nil {
			return err
		}
		q := u.Query()
		if u.Path != "/login/oauth/authorize" || q.Get("client_id") != "Iv1.synthetic" || q.Get("code_challenge_method") != "S256" || q.Get("scope") != "read:user" {
			t.Errorf("authorize URL %s", authorize)
		}
		// The browser: GitHub redirects to the loopback with the code and the state.
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=synthetic-code&state=" + url.QueryEscape(q.Get("state")))
			if err != nil {
				pages <- err.Error()
				return
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			pages <- string(body)
		}()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	username, err := p.Login(ctx, "synthetic-client-secret", announce)
	if err != nil || username != "octo" {
		t.Fatalf("Login = %q, %v", username, err)
	}
	if page := <-pages; !strings.Contains(page, "Copilot collegato") || !strings.Contains(page, `lang="it"`) {
		t.Errorf("callback page %q", page)
	}
	mu.Lock()
	defer mu.Unlock()
	if exchange["client_secret"] != "synthetic-client-secret" || exchange["code"] != "synthetic-code" || exchange["code_verifier"] == "" || !strings.HasPrefix(exchange["redirect_uri"], "http://127.0.0.1:") {
		t.Errorf("exchange %v", exchange)
	}
	if v, _ := store.Get(secrets.CopilotKey("copilot-1")); v != token {
		t.Errorf("stored %q", v)
	}
}

func TestLoginFailures(t *testing.T) {
	t.Parallel()
	gh := newFakeGitHub(t, nil)
	t.Run("missing client id or secret", func(t *testing.T) {
		t.Parallel()
		_, err := provider(t, gh.URL, &secrets.Memory{}).Login(context.Background(), "s", func(string) error { return nil })
		if asUsageError(t, err).Code != usage.CodeUnavailable {
			t.Fatal(err)
		}
	})
	t.Run("wrong state", func(t *testing.T) {
		t.Parallel()
		p := provider(t, gh.URL, &secrets.Memory{}, "--client-id", "x")
		_, err := p.Login(context.Background(), "s", func(authorize string) error {
			u, _ := url.Parse(authorize)
			go func() {
				resp, err := http.Get(u.Query().Get("redirect_uri") + "?code=c&state=forged")
				if err == nil {
					_ = resp.Body.Close()
				}
			}()
			return nil
		})
		if e := asUsageError(t, err); !strings.Contains(e.Message, "validation failed") {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("denied", func(t *testing.T) {
		t.Parallel()
		p := provider(t, gh.URL, &secrets.Memory{}, "--client-id", "x")
		_, err := p.Login(context.Background(), "s", func(authorize string) error {
			u, _ := url.Parse(authorize)
			go func() {
				resp, err := http.Get(u.Query().Get("redirect_uri") + "?error=access_denied&error_description=The+user+denied")
				if err == nil {
					_ = resp.Body.Close()
				}
			}()
			return nil
		})
		if e := asUsageError(t, err); !strings.Contains(e.Message, "The user denied") {
			t.Fatalf("%+v", e)
		}
	})
	t.Run("no authorization in time", func(t *testing.T) {
		t.Parallel()
		p := provider(t, gh.URL, &secrets.Memory{}, "--client-id", "x")
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := p.Login(ctx, "s", func(string) error { return nil })
		if asUsageError(t, err).Code != usage.CodeTimeout {
			t.Fatal(err)
		}
	})
}

// blockingStore never answers until released: a keyring waiting on an unlock prompt.
type blockingStore struct{ release chan struct{} }

func (b blockingStore) Get(string) (string, error) { <-b.release; return token, nil }
func (b blockingStore) Set(string, string) error   { <-b.release; return nil }
func (b blockingStore) Delete(string) error        { <-b.release; return nil }

func TestKeyringBoundedByTheDeadline(t *testing.T) {
	t.Parallel()
	store := blockingStore{release: make(chan struct{})}
	defer close(store.release)
	gh := newFakeGitHub(t, map[string]reply{"/user": {body: `{"login":"octo"}`}})
	p := provider(t, gh.URL, store)
	short := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 50*time.Millisecond)
	}
	ctx, cancel := short()
	defer cancel()
	if _, err := p.Fetch(ctx, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Fetch = %v; want a deadline", err)
	}
	ctx2, cancel2 := short()
	defer cancel2()
	if _, err := p.SetToken(ctx2, token, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("SetToken = %v; want a deadline", err)
	}
	ctx3, cancel3 := short()
	defer cancel3()
	if err := p.DeleteToken(ctx3); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("DeleteToken = %v; want a deadline", err)
	}
}

func TestLoginDeadlineDuringExchange(t *testing.T) {
	t.Parallel()
	exchangeStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/oauth/access_token" {
			close(exchangeStarted)
			// The server notices a client that went away only once the body is read.
			_, _ = io.Copy(io.Discard, r.Body)
			// GitHub never answers in time: the request must be cancelled at the deadline.
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
				t.Error("the code exchange was not cancelled at the login deadline")
			}
			return
		}
		_, _ = io.WriteString(w, `{"login":"octo"}`)
	}))
	defer server.Close()
	store := &secrets.Memory{}
	p := provider(t, server.URL, store, "--client-id", "x")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := p.Login(ctx, "s", func(authorize string) error {
		u, _ := url.Parse(authorize)
		go func() {
			resp, err := http.Get(u.Query().Get("redirect_uri") + "?code=c&state=" + url.QueryEscape(u.Query().Get("state")))
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	})
	<-exchangeStarted
	if asUsageError(t, err).Code != usage.CodeTimeout {
		t.Fatalf("Login = %v; want a timeout", err)
	}
	if _, err := store.Get(secrets.CopilotKey("copilot-1")); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("a token was stored after the login deadline")
	}
}
