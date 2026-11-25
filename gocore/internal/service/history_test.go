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
	"github.com/suppressio/aipermiler/gocore/internal/service"
)

// history serves the history endpoint at the snapshot fixture's moment, in
// the given calendar (nil is UTC).
func (a *apiFixture) history(t *testing.T, path string, now time.Time, loc *time.Location) *httptest.ResponseRecorder {
	t.Helper()
	if loc == nil {
		loc = time.UTC
	}
	r := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/v1/accounts/"+path, nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	h := service.NewHandler(service.HandlerConfig{
		Token: testToken, Host: testHost, Version: "1.2.3", ContractVersion: 1,
		Store: a.st, Loop: a.loop,
		Now: func() time.Time { return now }, Location: loc,
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type historyDoc struct {
	SchemaVersion int    `json:"schemaVersion"`
	Provider      string `json:"provider"`
	History       struct {
		Days    int `json:"days"`
		Windows []struct {
			ID    string `json:"id"`
			Daily []struct {
				Day          string  `json:"day"`
				Used         float64 `json:"used"`
				DayStartUsed float64 `json:"dayStartUsed"`
			} `json:"daily"`
			SamplesToday []struct {
				At   string  `json:"at"`
				Used float64 `json:"used"`
			} `json:"samplesToday"`
		} `json:"windows"`
	} `json:"history"`
}

func parseHistory(t *testing.T, w *httptest.ResponseRecorder) historyDoc {
	t.Helper()
	var doc historyDoc
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("status %d body %s: %v", w.Code, w.Body, err)
	}
	return doc
}

// The endpoint serves the fixture byte for byte: the account's history as
// the store keeps it, windows by id, local days, exact values.
func TestHistoryServesTheFixture(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	w := api.history(t, "a1/history", snapshotNow, nil)
	want := fixture(t, "service/history")
	if w.Code != http.StatusOK || w.Body.String() != string(want) {
		t.Errorf("status %d body\n%s\nwant\n%s", w.Code, w.Body, want)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// days=n keeps the daily points from n local days before today through
// today, as the diagnostic report did (7 by default); today's samples do not
// depend on it.
func TestHistoryDays(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	weekly := func(query string) ([]string, int) {
		doc := parseHistory(t, api.history(t, "a1/history"+query, snapshotNow, nil))
		for _, w := range doc.History.Windows {
			if w.ID == "weekly" {
				days := []string{}
				for _, p := range w.Daily {
					days = append(days, p.Day)
				}
				return days, len(w.SamplesToday)
			}
		}
		t.Fatal("no weekly window")
		return nil, 0
	}
	for query, want := range map[string][]string{
		"":        {"2026-11-01", "2026-11-02", "2026-11-03", "2026-11-04"},
		"?days=7": {"2026-11-01", "2026-11-02", "2026-11-03", "2026-11-04"},
		"?days=3": {"2026-11-01", "2026-11-02", "2026-11-03", "2026-11-04"},
		"?days=1": {"2026-11-03", "2026-11-04"},
		"?days=0": {"2026-11-04"},
	} {
		if got, samples := weekly(query); !reflect.DeepEqual(got, want) || samples != 3 {
			t.Errorf("%q: days %v samples %d; want %v and 3", query, got, samples, want)
		}
	}
	if doc := parseHistory(t, api.history(t, "a1/history?days=30", snapshotNow, nil)); doc.History.Days != 30 {
		t.Errorf("days = %d; want 30", doc.History.Days)
	}
}

// days must be an integer 0-3650 (the longest retention), given once; the
// account comes first, as on the snapshot.
func TestHistoryInvalidQuery(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	for _, bad := range []string{"?days=x", "?days=", "?days=-1", "?days=3651", "?days=1.5", "?days=7&days=7", "?days=%2B7"} {
		w := api.history(t, "a1/history"+bad, snapshotNow, nil)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"usage"`) {
			t.Errorf("%s: status %d body %s", bad, w.Code, w.Body)
		}
	}
	if w := api.history(t, "a1/history?days=3650", snapshotNow, nil); w.Code != http.StatusOK {
		t.Errorf("days=3650: status %d body %s", w.Code, w.Body)
	}
	if w := api.history(t, "nope/history?days=x", snapshotNow, nil); w.Code != http.StatusNotFound {
		t.Errorf("unknown account with a bad days: status %d body %s", w.Code, w.Body)
	}
}

// Unknown or disabled accounts get the usage endpoint's 404.
func TestHistoryUnknownOrDisabled(t *testing.T) {
	t.Parallel()
	off := pacedAccount()
	off.ID, off.Enabled = "off", false
	api := newAPI(t, off)
	for _, id := range []string{"nope", "off"} {
		if w := api.history(t, id+"/history", snapshotNow, nil); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"usage"`) {
			t.Errorf("%s: status %d body %s", id, w.Code, w.Body)
		}
	}
}

// Samples kept from the evening before (the store keeps the last 4 hours)
// are not today's; days and samples follow the service's local calendar.
func TestHistoryTodayIsLocal(t *testing.T) {
	t.Parallel()
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skip("no time zone database:", err)
	}
	local := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, rome) }
	account := fake("a1", nil)
	account.History = map[string]history.State{"w": {
		DailyUsage: []history.DailyPoint{{Day: local(3, 0, 0), Used: 5}, {Day: local(4, 0, 0), Used: 6, DayStartUsed: 5}},
		RecentSamples: []history.Reading{
			{At: local(3, 23, 30), Used: 5}, {At: local(4, 0, 30), Used: 6},
		},
	}}
	api := newAPI(t, account)
	doc := parseHistory(t, api.history(t, "a1/history", local(4, 1, 0), rome))
	if len(doc.History.Windows) != 1 {
		t.Fatalf("windows %+v", doc.History.Windows)
	}
	w := doc.History.Windows[0]
	if len(w.Daily) != 2 || w.Daily[0].Day != "2026-11-03" || w.Daily[1].Day != "2026-11-04" {
		t.Errorf("daily %+v", w.Daily)
	}
	if len(w.SamplesToday) != 1 || w.SamplesToday[0].At != "2026-11-03T23:30:00Z" {
		t.Errorf("samplesToday %+v; want only the one at 00:30 local (23:30 UTC)", w.SamplesToday)
	}
}

// An account without history, or without a good read, still answers: the
// history does not depend on the last read. Empty lists are [], never null.
func TestHistoryEmpty(t *testing.T) {
	t.Parallel()
	account := fake("a1", nil)
	account.History = map[string]history.State{"w": {}}
	api := newAPI(t, account, fake("a2", nil))
	for id, want := range map[string]string{
		"a1": `"windows":[{"id":"w","daily":[],"samplesToday":[]}]`,
		"a2": `"windows":[]`,
	} {
		w := api.history(t, id+"/history", snapshotNow, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: status %d body %s; want %s", id, w.Code, w.Body, want)
		}
	}
}

// The history is a view: serving it never writes the store.
func TestHistoryDoesNotWriteTheStore(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	before := api.st.Snapshot()
	api.history(t, "a1/history?days=30", snapshotNow, nil)
	if after := api.st.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Error("the history endpoint changed the store")
	}
}

// Only GET: the endpoint has no write method.
func TestHistoryIsReadOnly(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		if w := api.do(t, method, "/api/v1/accounts/a1/history", "{}"); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d", method, w.Code)
		}
	}
}

// The same gates as every endpoint: token, loopback Host, no Origin.
func TestHistoryNeedsTheSameGates(t *testing.T) {
	t.Parallel()
	api := newAPI(t, pacedAccount())
	for name, c := range map[string]struct {
		host, auth, origin string
		status             int
	}{
		"no token":    {testHost, "", "", http.StatusUnauthorized},
		"wrong token": {testHost, "Bearer nope", "", http.StatusUnauthorized},
		"origin":      {testHost, "Bearer " + testToken, "http://evil.example", http.StatusForbidden},
		"host":        {"evil.example:4242", "Bearer " + testToken, "", http.StatusForbidden},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://"+c.host+"/api/v1/accounts/a1/history", nil)
		r.Header.Set("Authorization", c.auth)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		service.NewHandler(service.HandlerConfig{Token: testToken, Host: testHost, Store: api.st, Loop: api.loop}).ServeHTTP(w, r)
		if w.Code != c.status || strings.Contains(w.Body.String(), "history") {
			t.Errorf("%s: status %d body %s", name, w.Code, w.Body)
		}
	}
}
