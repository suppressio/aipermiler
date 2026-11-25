package service_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/service"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// snapshotNow is the request's moment of the snapshot fixture.
var snapshotNow = time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func day(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }

// third is 100 / 300 * 100 computed at run time, as the snapshot computes a
// count window's utilization (a Go constant expression would be exact).
func third() float64 {
	used, total := 100.0, 300.0
	return used / total * 100
}

func at(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.UTC) }

// pacedAccount is the account of contract/v1/service/snapshot.json: a
// rolling-hours, a rolling-days and a billing-cycle count window with their
// history as the loop recorded it, and a count window without a total (no
// pacing, E14).
func pacedAccount() store.Account {
	lg := store.NewLastGood(usage.Usage{
		PlanTier: ptr("plus"),
		QuotaWindows: []usage.QuotaWindow{
			{ID: "five-hour", Label: "5 hours", PeriodType: usage.PeriodType("rolling-hours"), PeriodLength: ptr(5.0), Unit: usage.UnitPercentage, Used: 30, Total: ptr(100.0), ResetsAt: ptr(at(4, 17, 0))},
			{ID: "weekly", Label: "7 days", PeriodType: usage.PeriodType("rolling-days"), PeriodLength: ptr(7.0), Unit: usage.UnitPercentage, Used: 40, Total: ptr(100.0), ResetsAt: ptr(at(8, 9, 0))},
			{ID: "premium", Label: "Premium requests", PeriodType: usage.PeriodType("billing-cycle"), Unit: usage.UnitCount, Used: 100, Total: ptr(300.0), ResetsAt: ptr(day(30))},
			{ID: "extra", Label: "Extra requests", PeriodType: usage.PeriodType("billing-cycle"), Unit: usage.UnitCount, Used: 7},
		},
	}, at(4, 14, 55))
	return store.Account{
		ID: "a1", Provider: "fake", Label: "a1", Enabled: true,
		Schedule: store.Schedule{Days: [7]float64{1, 1, 1, 1, 1, 1, 1}, HoursPerDay: 8},
		History: map[string]history.State{
			"five-hour": {
				DailyUsage: []history.DailyPoint{{Day: day(4), Used: 30, DayStartUsed: 10}},
				RecentSamples: []history.Reading{
					{At: at(4, 13, 40), Used: 18}, {At: at(4, 13, 55), Used: 19}, {At: at(4, 14, 10), Used: 20},
					{At: at(4, 14, 25), Used: 21}, {At: at(4, 14, 40), Used: 28}, {At: at(4, 14, 55), Used: 30},
				},
			},
			"weekly": {
				DailyUsage: []history.DailyPoint{
					{Day: day(1), Used: 5, DayStartUsed: 0}, {Day: day(2), Used: 15, DayStartUsed: 5},
					{Day: day(3), Used: 30, DayStartUsed: 15}, {Day: day(4), Used: 40, DayStartUsed: 30},
				},
				RecentSamples: []history.Reading{{At: at(4, 14, 25), Used: 37}, {At: at(4, 14, 40), Used: 38.5}, {At: at(4, 14, 55), Used: 40}},
			},
			"premium": {
				DailyUsage:    []history.DailyPoint{{Day: day(3), Used: 25, DayStartUsed: 20}, {Day: day(4), Used: third(), DayStartUsed: 25}},
				RecentSamples: []history.Reading{{At: at(4, 14, 55), Used: third()}},
			},
		},
		LastGood: &lg,
	}
}

// snapshotAPI serves the snapshot endpoint at a fixed moment, in UTC, with a
// fixed tip draw.
func (a *apiFixture) snapshot(t *testing.T, query string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/v1/accounts/"+query, nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	for k, v := range header {
		r.Header.Set(k, v)
	}
	h := service.NewHandler(service.HandlerConfig{
		Token: testToken, Host: testHost, Version: "1.2.3", ContractVersion: 1,
		Store: a.st, Loop: a.loop,
		Now: func() time.Time { return snapshotNow }, Location: time.UTC, TipDraw: func() float64 { return 0 },
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type snapshotDoc struct {
	SchemaVersion int              `json:"schemaVersion"`
	Provider      string           `json:"provider"`
	FetchedAt     string           `json:"fetchedAt"`
	Stale         bool             `json:"stale"`
	StaleReason   *usage.StaleInfo `json:"staleReason"`
	Usage         json.RawMessage  `json:"usage"`
	Pacing        struct {
		Critical int `json:"critical"`
		Windows  []struct {
			ID        string   `json:"id"`
			Used      *float64 `json:"used"`
			Remaining *float64 `json:"remaining"`
			Chart     []struct {
				Day string `json:"day"`
			} `json:"chart"`
			Trend     *string `json:"trend"`
			Judgement struct {
				Verdict string `json:"verdict"`
			} `json:"judgement"`
		} `json:"windows"`
	} `json:"pacing"`
}

func parseSnapshot(t *testing.T, w *httptest.ResponseRecorder) snapshotDoc {
	t.Helper()
	var doc snapshotDoc
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("status %d body %s: %v", w.Code, w.Body, err)
	}
	return doc
}

// The endpoint serves the fixture byte for byte: the snapshot package's
// composition of the store's history, settings and last read, as of the
// request.
func TestSnapshotServesTheFixture(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	w := api.snapshot(t, "a1/snapshot", nil)
	want := fixture(t, "service/snapshot")
	if w.Code != http.StatusOK || w.Body.String() != string(want) {
		t.Errorf("status %d body\n%s\nwant\n%s", w.Code, w.Body, want)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// The snapshot document is the usage document of the same account plus
// pacing: same envelope, same usage, same freshness.
func TestSnapshotExtendsTheUsageDocument(t *testing.T) {
	t.Parallel()
	for name, lastErr := range map[string]error{
		"fresh":        nil,
		"stale":        &usage.Error{Code: usage.CodeUnavailable, Message: "Provider is temporarily unavailable"},
		"not read yet": refresh.ErrNotRead,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := newAPI(t, pacedAccount())
			if lastErr != nil {
				api.loop.errs["a1"] = lastErr
			}
			u := api.do(t, http.MethodGet, "/api/v1/accounts/a1/usage", "")
			var usageDoc map[string]json.RawMessage
			if err := json.Unmarshal(u.Body.Bytes(), &usageDoc); err != nil {
				t.Fatal(err)
			}
			w := api.snapshot(t, "a1/snapshot", nil)
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if w.Code != u.Code {
				t.Errorf("status %d; usage status %d", w.Code, u.Code)
			}
			pacing, ok := doc["pacing"]
			if !ok || string(pacing) == "null" {
				t.Errorf("no pacing in %s", w.Body)
			}
			delete(doc, "pacing")
			if !reflect.DeepEqual(doc, usageDoc) {
				t.Errorf("snapshot without pacing\n%v\nwant the usage document\n%v", doc, usageDoc)
			}
			if got := parseSnapshot(t, w); got.Stale != (lastErr != nil) {
				t.Errorf("stale = %v; want %v", got.Stale, lastErr != nil)
			}
		})
	}
}

// Without a good read the snapshot is the usage endpoint's error document,
// with its status: there is nothing to pace.
func TestSnapshotWithoutAGoodRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		err     error
		fixture string
		status  int
	}{
		{"not read yet", refresh.ErrNotRead, "service/error-not-read", http.StatusServiceUnavailable},
		{"no error and no good read", nil, "service/error-not-read", http.StatusServiceUnavailable},
		{"signed out", &usage.Error{Code: usage.CodeSignedOut, Message: "Sign in required"}, "error-signed-out", http.StatusBadGateway},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			api := newAPI(t, fake("a1", nil))
			if c.err != nil {
				api.loop.errs["a1"] = c.err
			}
			w := api.snapshot(t, "a1/snapshot", nil)
			if w.Code != c.status || w.Body.String() != string(fixture(t, c.fixture)) {
				t.Errorf("status %d body %s; want %d %s", w.Code, w.Body, c.status, fixture(t, c.fixture))
			}
		})
	}
}

// Values are exact (I-4): a count window's utilization keeps every digit.
func TestSnapshotValuesAreExact(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	doc := parseSnapshot(t, api.snapshot(t, "a1/snapshot", nil))
	for _, w := range doc.Pacing.Windows {
		switch w.ID {
		case "premium":
			if want := third(); w.Used == nil || *w.Used != want || w.Remaining == nil || *w.Remaining != 100-want {
				t.Errorf("premium used %v remaining %v; want %v and %v", w.Used, w.Remaining, want, 100-want)
			}
		case "extra": // no total: undefined, never 0 (I-5)
			if w.Used != nil || w.Remaining != nil || w.Judgement.Verdict != "no-pacing" {
				t.Errorf("extra used %v remaining %v verdict %q", w.Used, w.Remaining, w.Judgement.Verdict)
			}
		}
	}
	if len(doc.Pacing.Windows) != 4 {
		t.Errorf("windows = %d; want 4", len(doc.Pacing.Windows))
	}
}

// The instant trend (D-3.28, moved here from #303) is in each window: the
// pace's direction on a fresh read, null without one (too few samples) and
// null on every window while the last read failed (TR-8): the values shown
// are then the last known ones.
func TestSnapshotTrend(t *testing.T) {
	t.Parallel()
	trends := func(lastErr error) map[string]*string {
		api := newAPI(t, pacedAccount())
		if lastErr != nil {
			api.loop.errs["a1"] = lastErr
		}
		out := map[string]*string{}
		for _, w := range parseSnapshot(t, api.snapshot(t, "a1/snapshot", nil)).Pacing.Windows {
			out[w.ID] = w.Trend
		}
		return out
	}
	str := func(p *string) string {
		if p == nil {
			return "null"
		}
		return *p
	}
	// five-hour: 4 pp/h at 14:30, 11 pp/h at 15:00, θ = 2 × (1 + 1/0.75).
	fresh := trends(nil)
	for id, want := range map[string]string{"five-hour": "accelerating", "premium": "null", "extra": "null"} {
		if got := str(fresh[id]); got != want {
			t.Errorf("fresh %s: trend %s; want %s", id, got, want)
		}
	}
	for name, lastErr := range map[string]error{
		"failed read":  &usage.Error{Code: usage.CodeUnavailable, Message: "Provider is temporarily unavailable"},
		"not read yet": refresh.ErrNotRead,
	} {
		for id, trend := range trends(lastErr) {
			if trend != nil {
				t.Errorf("%s %s: trend %s; want null", name, id, *trend)
			}
		}
	}
}

// Re-recording a fresh read the loop already recorded is a no-op: apart from
// the trend, its pacing is the one of the same read served as stale, which is
// never recorded. (That re-recording leaves the history itself unchanged is
// asserted in internal/pacing/snapshot, TestBuildOfAReadAlreadyRecordedKeepsTheHistory.)
func TestSnapshotFreshReadIsNotRecordedAgain(t *testing.T) {
	t.Parallel()
	pacing := func(lastErr error) []map[string]json.RawMessage {
		api := newAPI(t, pacedAccount())
		if lastErr != nil {
			api.loop.errs["a1"] = lastErr
		}
		var doc struct {
			Pacing struct {
				Windows []map[string]json.RawMessage `json:"windows"`
			} `json:"pacing"`
		}
		if err := json.Unmarshal(api.snapshot(t, "a1/snapshot", nil).Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		for _, w := range doc.Pacing.Windows {
			delete(w, "trend")
		}
		return doc.Pacing.Windows
	}
	fresh, stale := pacing(nil), pacing(refresh.ErrNotRead)
	if !reflect.DeepEqual(fresh, stale) {
		t.Errorf("fresh\n%v\nstale\n%v", fresh, stale)
	}
}

// The snapshot is a view: serving it never writes the store.
func TestSnapshotDoesNotWriteTheStore(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	before := api.st.Snapshot()
	api.snapshot(t, "a1/snapshot", nil)
	if after := api.st.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Error("the snapshot changed the store")
	}
}

// chartDays picks the chart span and the rating span, 7 (default) or 30.
func TestSnapshotChartDays(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	weekly := func(query string) int {
		for _, w := range parseSnapshot(t, api.snapshot(t, "a1/snapshot"+query, nil)).Pacing.Windows {
			if w.ID == "weekly" {
				return len(w.Chart)
			}
		}
		t.Fatal("no weekly window")
		return 0
	}
	if week, month, def := weekly("?chartDays=7"), weekly("?chartDays=30"), weekly(""); week != def || month <= week {
		t.Errorf("chart slots: 7 → %d, 30 → %d, default → %d", week, month, def)
	}
	for _, bad := range []string{"?chartDays=14", "?chartDays=x", "?chartDays=", "?chartDays=7&chartDays=99", "?chartDays=7&chartDays=7"} {
		w := api.snapshot(t, "a1/snapshot"+bad, nil)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"usage"`) {
			t.Errorf("%s: status %d body %s", bad, w.Code, w.Body)
		}
	}
	// The account comes first: an unknown one is 404 whatever the query.
	if w := api.snapshot(t, "nope/snapshot?chartDays=14", nil); w.Code != http.StatusNotFound {
		t.Errorf("unknown account with a bad chartDays: status %d body %s", w.Code, w.Body)
	}
}

// Unknown or disabled accounts get the usage endpoint's 404.
func TestSnapshotUnknownOrDisabled(t *testing.T) {
	t.Parallel()
	off := pacedAccount()
	off.ID, off.Enabled = "off", false
	api := newAPI(t, off)
	for _, id := range []string{"nope", "off"} {
		if w := api.snapshot(t, id+"/snapshot", nil); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"usage"`) {
			t.Errorf("%s: status %d body %s", id, w.Code, w.Body)
		}
	}
}

// The same gates as every endpoint: token, loopback Host, no Origin.
func TestSnapshotNeedsTheSameGates(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	for name, c := range map[string]struct {
		header map[string]string
		status int
	}{
		"no token":    {map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		"wrong token": {map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		"origin":      {map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		"host":        {map[string]string{"Host": "evil.example:4242"}, http.StatusForbidden},
	} {
		h := c.header
		if host, ok := h["Host"]; ok {
			delete(h, "Host")
			r := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/v1/accounts/a1/snapshot", nil)
			r.Header.Set("Authorization", "Bearer "+testToken)
			w := httptest.NewRecorder()
			service.NewHandler(service.HandlerConfig{Token: testToken, Host: testHost, Store: api.st, Loop: api.loop}).ServeHTTP(w, r)
			if w.Code != c.status {
				t.Errorf("%s: status %d", name, w.Code)
			}
			continue
		}
		if w := api.snapshot(t, "a1/snapshot", h); w.Code != c.status || strings.Contains(w.Body.String(), "pacing") {
			t.Errorf("%s: status %d body %s", name, w.Code, w.Body)
		}
	}
}

// Without a clock, a calendar or a draw the handler uses the real ones.
func TestSnapshotDefaults(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	r := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/v1/accounts/a1/snapshot", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	service.NewHandler(service.HandlerConfig{Token: testToken, Host: testHost, Store: api.st, Loop: api.loop}).ServeHTTP(w, r)
	if doc := parseSnapshot(t, w); w.Code != http.StatusOK || len(doc.Pacing.Windows) != 4 {
		t.Errorf("status %d body %s", w.Code, w.Body)
	}
}
