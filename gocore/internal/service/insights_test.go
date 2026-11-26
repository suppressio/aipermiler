package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/insights"
	"github.com/suppressio/aipermiler/gocore/internal/service"
	"github.com/suppressio/aipermiler/gocore/internal/store"
)

// insightsAccount is the fixture's account as the one Claude account that
// counts this machine's Claude Code sessions (#336).
func insightsAccount() store.Account {
	a := pacedAccount()
	a.Provider, a.LocalInsights = "claude", true
	return a
}

// localTokens are the output tokens of the weekly window's four days: with
// its deltas 5, 10, 15 and 10 pp, 1000 tokens per pp in both halves.
func localTokens() *insights.Result {
	return &insights.Result{
		Daily: []insights.DailyTokens{
			{Date: "2026-11-01", OutputTokens: 5000}, {Date: "2026-11-02", OutputTokens: 10000},
			{Date: "2026-11-03", OutputTokens: 15000}, {Date: "2026-11-04", OutputTokens: 10000},
		},
		FirstSessionStartByDay: map[string]string{},
	}
}

// fakeInsights counts the computations and records their window.
type fakeInsights struct {
	mu     sync.Mutex
	calls  []int
	result *insights.Result
	err    error
	done   chan struct{} // receives after every computation, when not nil
	gate   chan struct{} // when not nil, a computation waits for it
}

func (f *fakeInsights) compute(_ context.Context, windowDays int, _ time.Time) (*insights.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, windowDays)
	f.mu.Unlock()
	if f.gate != nil {
		<-f.gate
	}
	if f.done != nil {
		defer func() { f.done <- struct{}{} }()
	}
	return f.result, f.err
}

func (f *fakeInsights) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type insightsDoc struct {
	Pacing struct {
		Windows []struct {
			ID         string `json:"id"`
			TokenYield *struct {
				TokensPerPercent int64    `json:"tokensPerPercent"`
				TrendPercent     *float64 `json:"trendPercent"`
				DaysCompared     int      `json:"daysCompared"`
			} `json:"tokenYield"`
			ConsumptionCause *json.RawMessage `json:"consumptionCause"`
		} `json:"windows"`
	} `json:"pacing"`
}

// insightsHandler is one handler, as the service keeps one: its cache lives
// across requests. now is read at each request.
func insightsHandler(api *apiFixture, f *fakeInsights, now *time.Time) http.Handler {
	return service.NewHandler(service.HandlerConfig{
		Token: testToken, Host: testHost, Version: "1.2.3", ContractVersion: 1,
		Store: api.st, Loop: api.loop,
		Now: func() time.Time { return *now }, Location: time.UTC, TipDraw: func() float64 { return 0 },
		LocalInsights: f.compute,
	})
}

func getInsights(t *testing.T, h http.Handler, query string) insightsDoc {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/v1/accounts/a1/snapshot"+query, nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
	var doc insightsDoc
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// D-3.26 and D-3.27 in the snapshot (#340): per window, null below the
// thresholds (I-5), never 0.
func TestSnapshotLocalInsights(t *testing.T) {
	t.Parallel()
	api := newAPI(t, insightsAccount())
	f := &fakeInsights{result: localTokens()}
	now := snapshotNow
	doc := getInsights(t, insightsHandler(api, f, &now), "")
	byID := map[string]int{}
	for i, w := range doc.Pacing.Windows {
		byID[w.ID] = i
	}
	weekly := doc.Pacing.Windows[byID["weekly"]]
	if y := weekly.TokenYield; y == nil || y.TokensPerPercent != 1000 || y.TrendPercent == nil || *y.TrendPercent != 0 || y.DaysCompared != 4 {
		t.Errorf("weekly tokenYield = %+v, want 1000 tokens/pp, trend 0, 4 days", y)
	}
	if weekly.ConsumptionCause != nil {
		t.Errorf("weekly consumptionCause = %s, want null below 5 days", *weekly.ConsumptionCause)
	}
	for _, id := range []string{"five-hour", "premium", "extra"} {
		if w := doc.Pacing.Windows[byID[id]]; w.TokenYield != nil || w.ConsumptionCause != nil {
			t.Errorf("%s: yield %+v cause %v, want null (rolling hours, 2 days, no utilization)", id, w.TokenYield, w.ConsumptionCause)
		}
	}
	// The analysis window is the chart's span plus one day, as Electron's.
	if len(f.calls) != 1 || f.calls[0] != 8 {
		t.Errorf("computations %v, want one over 8 days", f.calls)
	}
}

// Only the opted-in Claude account reads the session files.
func TestSnapshotLocalInsightsOptIn(t *testing.T) {
	t.Parallel()
	off := insightsAccount()
	off.LocalInsights = false
	other := pacedAccount()
	other.LocalInsights = true // not Claude: the settings refuse it, the snapshot ignores it
	for name, account := range map[string]store.Account{"not opted in": off, "not claude": other} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := newAPI(t, account)
			f := &fakeInsights{result: localTokens()}
			now := snapshotNow
			doc := getInsights(t, insightsHandler(api, f, &now), "")
			for _, w := range doc.Pacing.Windows {
				if w.TokenYield != nil {
					t.Errorf("%s: tokenYield %+v, want null", w.ID, w.TokenYield)
				}
			}
			if f.count() != 0 {
				t.Errorf("%d computations, want none", f.count())
			}
		})
	}
}

// The session files are read at most every 2 hours per analysis window, as
// Electron does; a stale result is served while a new one is computed.
func TestSnapshotLocalInsightsCache(t *testing.T) {
	t.Parallel()
	api := newAPI(t, insightsAccount())
	f := &fakeInsights{result: localTokens(), done: make(chan struct{}, 4)}
	now := snapshotNow
	h := insightsHandler(api, f, &now)

	getInsights(t, h, "")
	<-f.done
	now = now.Add(time.Hour)
	getInsights(t, h, "")
	if f.count() != 1 {
		t.Fatalf("%d computations within 2 hours, want 1", f.count())
	}

	getInsights(t, h, "?chartDays=30")
	<-f.done
	if f.calls[1] != 31 {
		t.Errorf("30-day chart computed over %d days, want 31", f.calls[1])
	}

	now = now.Add(time.Hour + time.Minute)
	f.mu.Lock()
	f.result = &insights.Result{FirstSessionStartByDay: map[string]string{}}
	f.mu.Unlock()
	doc := getInsights(t, h, "")
	if doc.Pacing.Windows[1].TokenYield == nil {
		t.Error("expired result not served while the new one is computed")
	}
	<-f.done
	if f.count() != 3 {
		t.Fatalf("%d computations, want 3 (the expired one recomputed)", f.count())
	}
	// The computation has returned; its result is stored just after.
	deadline := time.Now().Add(5 * time.Second)
	for getInsights(t, h, "").Pacing.Windows[1].TokenYield != nil {
		if time.Now().After(deadline) {
			t.Fatal("the recomputed result is not used")
		}
		time.Sleep(time.Millisecond)
	}
}

// Requests that arrive while the first count runs wait for it instead of
// scanning the session files again.
func TestSnapshotLocalInsightsSharesTheRunningCount(t *testing.T) {
	t.Parallel()
	api := newAPI(t, insightsAccount())
	f := &fakeInsights{result: localTokens(), gate: make(chan struct{})}
	now := snapshotNow
	h := insightsHandler(api, f, &now)
	var wg sync.WaitGroup
	results := make([]insightsDoc, 3)
	for i := range results {
		wg.Go(func() { results[i] = getInsights(t, h, "") })
	}
	for f.count() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let the other requests arrive
	close(f.gate)
	wg.Wait()
	if f.count() != 1 {
		t.Errorf("%d computations for 3 requests, want 1", f.count())
	}
	for i, doc := range results {
		if doc.Pacing.Windows[1].TokenYield == nil {
			t.Errorf("request %d: no tokenYield", i)
		}
	}
}

// A failed computation never fails the snapshot: it is served without the
// local insights (or with the last result, when there is one).
func TestSnapshotLocalInsightsFailure(t *testing.T) {
	t.Parallel()
	api := newAPI(t, insightsAccount())
	f := &fakeInsights{err: errors.New("projects folder unreadable")}
	now := snapshotNow
	doc := getInsights(t, insightsHandler(api, f, &now), "")
	for _, w := range doc.Pacing.Windows {
		if w.TokenYield != nil {
			t.Errorf("%s: tokenYield %+v, want null", w.ID, w.TokenYield)
		}
	}
}
