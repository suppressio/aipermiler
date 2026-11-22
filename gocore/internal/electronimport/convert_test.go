package electronimport_test

import (
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/electronimport"
	"github.com/suppressio/aipermiler/gocore/internal/store"
)

func convertFixture(t *testing.T) (store.State, electronimport.Report) {
	t.Helper()
	state, report, err := electronimport.Convert(fixture(t, "plaintext.json"), time.UTC)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return state, report
}

func TestConvertSettings(t *testing.T) {
	t.Parallel()
	state, _ := convertFixture(t)
	want := store.Settings{RefreshIntervalMinutes: 15, RetentionDays: 60, NotificationThresholdPercent: 90}
	if state.Settings != want {
		t.Fatalf("settings = %+v, want %+v", state.Settings, want)
	}
}

func TestConvertAccounts(t *testing.T) {
	t.Parallel()
	state, _ := convertFixture(t)
	if len(state.Accounts) != 3 {
		t.Fatalf("accounts = %d, want 3", len(state.Accounts))
	}
	claude := state.Account("claude")
	if claude == nil || claude.Provider != "claude" || claude.Label != "Claude work" || !claude.Enabled {
		t.Fatalf("claude = %+v", claude)
	}
	if claude.RenewalDay != 5 {
		t.Errorf("renewal day = %d, want 5", claude.RenewalDay)
	}
	// Sunday first: wed is half, sat and sun off.
	if claude.Schedule != (store.Schedule{Enabled: true, Days: [7]float64{0, 1, 1, 0.5, 1, 1, 0}, HoursPerDay: 7.5}) {
		t.Errorf("schedule = %+v", claude.Schedule)
	}
	if claude.BinaryPath == nil || *claude.BinaryPath != "/opt/claude/bin/claude" {
		t.Errorf("binary path = %v", claude.BinaryPath)
	}
	copilot := state.Account("copilot-1")
	if copilot == nil || copilot.Enabled || copilot.Host != "acme.ghe.com" || copilot.Scope != "organization" || copilot.ManualQuota != 300 {
		t.Fatalf("copilot = %+v", copilot)
	}
	if copilot.Schedule.Enabled {
		t.Errorf("copilot schedule should be disabled: %+v", copilot.Schedule)
	}
	codex := state.Account("codex")
	if codex == nil || codex.RenewalDay != 0 || codex.BinaryPath != nil {
		t.Fatalf("codex = %+v", codex)
	}
}

func TestConvertNeverCarriesSecrets(t *testing.T) {
	t.Parallel()
	state, _ := convertFixture(t)
	for _, a := range state.Accounts {
		if strings.Contains(a.ID+a.Label+a.Host, "SYNTHETIC-TOKEN") {
			t.Fatalf("token leaked into %+v", a)
		}
	}
}

func TestConvertHistory(t *testing.T) {
	t.Parallel()
	state, _ := convertFixture(t)
	h := state.Account("claude").History["five_hour"]
	if len(h.DailyUsage) != 3 {
		t.Fatalf("daily = %+v", h.DailyUsage)
	}
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	// Sorted by day; a point without dayStartUsed takes the previous day's used.
	for i, want := range []struct {
		day         time.Time
		used, start float64
	}{{day(1), 10.5, 0}, {day(2), 40, 10}, {day(3), 55, 40}} {
		got := h.DailyUsage[i]
		if !got.Day.Equal(want.day) || got.Used != want.used || got.DayStartUsed != want.start {
			t.Errorf("daily[%d] = %+v, want %+v", i, got, want)
		}
	}
	if len(h.RecentSamples) != 2 || h.RecentSamples[0].Used != 45 || h.RecentSamples[1].Used != 50 {
		t.Fatalf("samples = %+v", h.RecentSamples)
	}
	if got := state.Account("codex").History["codex-5h"].DailyUsage; len(got) != 1 || got[0].DayStartUsed != 2 {
		t.Fatalf("codex daily = %+v", got)
	}
}

func TestConvertDaysUseTheGivenLocation(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("test", 5*3600)
	state, _, err := electronimport.Convert(fixture(t, "plaintext.json"), loc)
	if err != nil {
		t.Fatal(err)
	}
	got := state.Account("claude").History["five_hour"].DailyUsage[0].Day
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("day = %v, want local midnight %v", got, want)
	}
}

func TestConvertLastGood(t *testing.T) {
	t.Parallel()
	state, _ := convertFixture(t)
	lg := state.Account("claude").LastGood
	if lg == nil || !lg.UpdatedAt.Equal(time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("lastGood = %+v", lg)
	}
	if lg.PlanTier == nil || *lg.PlanTier != "max" || lg.SubscriptionRenewsAt == nil {
		t.Errorf("lastGood = %+v", lg)
	}
	if len(lg.QuotaWindows) != 1 || lg.QuotaWindows[0].ID != "five_hour" || lg.QuotaWindows[0].Used != 50 ||
		lg.QuotaWindows[0].ResetsAt == nil || *lg.QuotaWindows[0].PeriodLength != 5 {
		t.Errorf("windows = %+v", lg.QuotaWindows)
	}
	if state.Account("codex").LastGood != nil {
		t.Error("codex has no lastGood")
	}
}

func TestConvertReportsWhatItSkips(t *testing.T) {
	t.Parallel()
	_, report := convertFixture(t)
	// bad date, bad timestamp, point of an account that is gone.
	if report.Skipped != 3 {
		t.Fatalf("skipped = %d (%v), want 3", report.Skipped, report.Warnings)
	}
}

func TestConvertRejectsUnusableDocuments(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]string{
		"not json":       `nope`,
		"not an object":  `[]`,
		"no accounts":    `{"ui":{}}`,
		"accounts wrong": `{"accounts":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := electronimport.Convert([]byte(doc), time.UTC); err == nil {
				t.Fatal("Convert succeeded")
			}
		})
	}
}

func TestConvertEmptyAccountsIsFine(t *testing.T) {
	t.Parallel()
	state, _, err := electronimport.Convert([]byte(`{"accounts":[]}`), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if state.Settings != store.Empty().Settings || len(state.Accounts) != 0 {
		t.Fatalf("state = %+v", state)
	}
}

func TestConvertSkipsUnknownProviderAndDuplicates(t *testing.T) {
	t.Parallel()
	doc := `{"accounts":[
	 {"id":"a","provider":"gemini","label":"x","enabled":true},
	 {"id":"b","provider":"codex","label":"one","enabled":true},
	 {"id":"b","provider":"codex","label":"two","enabled":true},
	 {"provider":"codex","label":"no id"}]}`
	state, report, err := electronimport.Convert([]byte(doc), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Accounts) != 1 || state.Accounts[0].Label != "one" || report.Skipped != 3 {
		t.Fatalf("accounts = %+v, skipped = %d", state.Accounts, report.Skipped)
	}
}
