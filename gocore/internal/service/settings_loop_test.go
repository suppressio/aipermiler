package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/service"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// blockingFetcher reports each read on reading and answers once released:
// fresh values for nil, the error otherwise.
type blockingFetcher struct {
	reading chan string
	release chan error
}

func (f blockingFetcher) Fetch(ctx context.Context, account store.Account) (usage.Usage, error) {
	f.reading <- account.ID
	select {
	case err := <-f.release:
		if err != nil {
			return usage.Usage{}, err
		}
		return usage.Usage{QuotaWindows: []usage.QuotaWindow{{ID: "w", Label: "w", PeriodType: usage.PeriodRollingHours, Unit: usage.UnitPercentage, Used: 5}}}, nil
	case <-ctx.Done():
		return usage.Usage{}, ctx.Err()
	}
}

// An account disabled and enabled again through the settings, with the real
// loop: until its new read ends it is served as not read yet, never as fresh
// nor with the error it had before (#284, the gap left by #248).
func TestEnablingAnAccountIsNotServedFreshUntilItsRead(t *testing.T) {
	t.Parallel()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	account := store.Account{ID: "a", Provider: "codex", Label: "a", Enabled: true,
		Schedule: store.Schedule{Days: [7]float64{1, 1, 1, 1, 1, 1, 1}, HoursPerDay: 8}, LastGood: lastGood(t, "usage-ok")}
	if err := st.Update(func(s *store.State) error { s.Accounts = []store.Account{account}; return nil }); err != nil {
		t.Fatal(err)
	}
	fetch := blockingFetcher{reading: make(chan string, 4), release: make(chan error)}
	loop := refresh.New(refresh.Config{Store: st, Fetcher: fetch})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = loop.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	h := service.NewHandler(service.HandlerConfig{Token: testToken, Host: testHost, Store: st, Loop: loop})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+testHost+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	view := func() (v struct {
		Stale       bool             `json:"stale"`
		StaleReason *usage.StaleInfo `json:"staleReason"`
	},
	) {
		t.Helper()
		w := do(http.MethodGet, "/api/v1/accounts/a/usage", "")
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatalf("usage: status %d body %s", w.Code, w.Body)
		}
		return v
	}
	put := func(enabled string) {
		t.Helper()
		body := `{"schemaVersion":1,"settings":{"refreshIntervalMinutes":30,"retentionDays":90,"notificationThresholdPercent":80},"accounts":[` +
			`{"id":"a","provider":"codex","label":"a","enabled":` + enabled + `,"renewalDay":0,"schedule":{"enabled":false,"days":[1,1,1,1,1,1,1],"hoursPerDay":8}}]}`
		if w := do(http.MethodPut, "/api/v1/settings", body); w.Code != http.StatusOK {
			t.Fatalf("settings: status %d body %s", w.Code, w.Body)
		}
	}

	// The start read fails: the account is served stale with that error.
	<-fetch.reading
	fetch.release <- &usage.Error{Code: usage.CodeSignedOut, Message: "Sign in required"}
	waitFor(t, func() bool {
		err := loop.LastError("a")
		return err != nil && !errors.Is(err, refresh.ErrNotRead)
	})
	if v := view(); !v.Stale || v.StaleReason.Code != usage.CodeSignedOut {
		t.Fatalf("after the failed read: %+v", v)
	}

	put("false")
	put("true")
	if got := <-fetch.reading; got != "a" {
		t.Fatalf("read %q", got)
	}
	// The new read is in progress: neither fresh nor the old error.
	if v := view(); !v.Stale || v.StaleReason.Code != usage.CodeUnavailable || v.StaleReason.Reason != "not_read_yet" {
		t.Errorf("during the new read: %+v; want stale, not_read_yet", v)
	}
	fetch.release <- nil
	waitFor(t, func() bool { return loop.LastError("a") == nil })
	if v := view(); v.Stale {
		t.Errorf("after the new read: %+v; want fresh", v)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition never met")
}
