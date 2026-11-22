package electronimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// Report says what Convert left out. Warnings name accounts and windows by id
// and never carry values, so they are safe to log.
type Report struct {
	Skipped  int
	Warnings []string
}

func (r *Report) skip(format string, args ...any) {
	r.Skipped++
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// errNoAccounts: the document is not Electron's AppSettings.
var errNoAccounts = errors.New("no accounts array: not the app's settings")

// The parts of Electron's AppSettings (types/index.ts) the core's store takes.
// Electron normalizes the file at every start, so the shapes are reliable; a
// piece that still does not fit is skipped and reported, not guessed.
type electronSettings struct {
	Accounts *[]json.RawMessage `json:"accounts"`
	UI       struct {
		RefreshIntervalMinutes       *float64 `json:"refreshIntervalMinutes"`
		NotificationThresholdPercent *float64 `json:"notificationThresholdPercent"`
	} `json:"ui"`
	History struct {
		RetentionDays *float64                   `json:"retentionDays"`
		DailyUsage    []json.RawMessage          `json:"dailyUsage"`
		RecentSamples []json.RawMessage          `json:"recentSamples"`
		LastGood      map[string]json.RawMessage `json:"lastGood"`
	} `json:"history"`
}

type electronAccount struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	Label        string `json:"label"`
	Enabled      bool   `json:"enabled"`
	Subscription struct {
		RenewalRule struct {
			Type string `json:"type"`
			Day  int    `json:"day"`
		} `json:"renewalRule"`
	} `json:"subscription"`
	WorkSchedule struct {
		Enabled     bool              `json:"enabled"`
		Days        map[string]string `json:"days"`
		HoursPerDay float64           `json:"hoursPerDay"`
	} `json:"workSchedule"`
	AccountScope string  `json:"accountScope"`
	Host         string  `json:"host"`
	ManualQuota  float64 `json:"manualQuota"`
	BinaryPath   *string `json:"binaryPath"`
}

type electronDaily struct {
	Date         string   `json:"date"`
	AccountID    string   `json:"accountId"`
	WindowID     string   `json:"windowId"`
	Used         float64  `json:"used"`
	DayStartUsed *float64 `json:"dayStartUsed"`
}

type electronSample struct {
	Timestamp time.Time `json:"timestamp"`
	AccountID string    `json:"accountId"`
	WindowID  string    `json:"windowId"`
	Used      float64   `json:"used"`
}

type electronLastGood struct {
	LastUpdatedAt        time.Time      `json:"lastUpdatedAt"`
	PlanTier             *string        `json:"planTier"`
	SubscriptionRenewsAt *time.Time     `json:"subscriptionRenewsAt"`
	QuotaWindows         []store.Window `json:"quotaWindows"`
}

// weekdays in the order of Electron's days object, as the store's Sunday-first
// array.
var weekdays = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// Convert maps Electron's decrypted AppSettings onto the core's store state.
// Daily points are local days of loc, the time zone the app ran in.
// Not imported: Copilot's token (the keyring owns it since #179), the Claude and
// Codex session data (purged since #140 and #178), UI, tray, update and
// diagnostics settings, the advisor and local-insights caches.
func Convert(plaintext []byte, loc *time.Location) (store.State, Report, error) {
	var src electronSettings
	if err := json.Unmarshal(plaintext, &src); err != nil {
		return store.State{}, Report{}, fmt.Errorf("decode settings: %w", err)
	}
	if src.Accounts == nil {
		return store.State{}, Report{}, errNoAccounts
	}
	state, report := store.Empty(), Report{}
	state.Settings.RefreshIntervalMinutes = positiveInt(src.UI.RefreshIntervalMinutes, state.Settings.RefreshIntervalMinutes)
	state.Settings.RetentionDays = positiveInt(src.History.RetentionDays, state.Settings.RetentionDays)
	state.Settings.NotificationThresholdPercent = positiveInt(src.UI.NotificationThresholdPercent, state.Settings.NotificationThresholdPercent)

	for _, raw := range *src.Accounts {
		account, ok := convertAccount(raw, &state, &report)
		if ok {
			state.Accounts = append(state.Accounts, account)
		}
	}
	convertHistory(src, &state, &report, loc)
	for id, raw := range src.History.LastGood {
		account := state.Account(id)
		if account == nil {
			report.skip("last good read of unknown account %q", id)
			continue
		}
		var lg electronLastGood
		if err := json.Unmarshal(raw, &lg); err != nil || lg.LastUpdatedAt.IsZero() {
			report.skip("last good read of account %q is malformed", id)
			continue
		}
		account.LastGood = &store.LastGood{
			UpdatedAt: lg.LastUpdatedAt, PlanTier: lg.PlanTier,
			SubscriptionRenewsAt: lg.SubscriptionRenewsAt, QuotaWindows: lg.QuotaWindows,
		}
		if account.LastGood.QuotaWindows == nil {
			account.LastGood.QuotaWindows = []store.Window{}
		}
		account.LastGood.QuotaWindows = slices.DeleteFunc(account.LastGood.QuotaWindows, func(w store.Window) bool {
			bad := !validWindow(w)
			if bad {
				report.skip("window %q of account %q has an unknown type", w.ID, id)
			}
			return bad
		})
	}
	return state, report, nil
}

func validWindow(w store.Window) bool {
	switch w.PeriodType {
	case usage.PeriodRollingHours, usage.PeriodRollingDays, usage.PeriodBillingCycle:
	default:
		return false
	}
	return (w.Unit == usage.UnitPercentage || w.Unit == usage.UnitCount) && w.ID != ""
}

func positiveInt(v *float64, fallback int) int {
	if v == nil || *v < 1 || *v != math.Trunc(*v) || *v > math.MaxInt32 {
		return fallback
	}
	return int(*v)
}

func convertAccount(raw json.RawMessage, state *store.State, report *Report) (store.Account, bool) {
	var src electronAccount
	if err := json.Unmarshal(raw, &src); err != nil || src.ID == "" {
		report.skip("an account is malformed or has no id")
		return store.Account{}, false
	}
	switch src.Provider {
	case "claude", "codex", "copilot":
	default:
		report.skip("account %q has an unknown provider", src.ID)
		return store.Account{}, false
	}
	if state.Account(src.ID) != nil {
		report.skip("account %q appears twice", src.ID)
		return store.Account{}, false
	}
	account := store.Account{
		ID: src.ID, Provider: src.Provider, Label: src.Label, Enabled: src.Enabled,
		Schedule: store.Schedule{Enabled: src.WorkSchedule.Enabled, HoursPerDay: src.WorkSchedule.HoursPerDay},
		History:  map[string]history.State{},
	}
	if src.Subscription.RenewalRule.Type == "dayOfMonth" {
		account.RenewalDay = src.Subscription.RenewalRule.Day
	}
	if account.Schedule.HoursPerDay <= 0 {
		account.Schedule.HoursPerDay = 8
	}
	for i, name := range weekdays {
		account.Schedule.Days[i] = dayWeight(src.WorkSchedule.Days[name])
	}
	switch src.Provider {
	case "copilot":
		account.Host, account.Scope, account.ManualQuota = src.Host, src.AccountScope, src.ManualQuota
	default:
		if src.BinaryPath != nil && *src.BinaryPath != "" {
			account.BinaryPath = src.BinaryPath
		}
	}
	return account, true
}

func dayWeight(status string) float64 {
	switch status {
	case "full":
		return 1
	case "half":
		return 0.5
	default:
		return 0
	}
}

func convertHistory(src electronSettings, state *store.State, report *Report, loc *time.Location) {
	type key struct{ account, window string }
	daily := map[key][]history.DailyPoint{}
	hasBaseline := map[key]map[time.Time]bool{}
	samples := map[key][]history.Reading{}
	for _, raw := range src.History.DailyUsage {
		var p electronDaily
		if err := json.Unmarshal(raw, &p); err != nil {
			report.skip("a daily point is malformed")
			continue
		}
		day, parseErr := time.ParseInLocation(time.DateOnly, p.Date, loc)
		if parseErr != nil || p.Used < 0 {
			report.skip("a daily point of %q has an invalid date or value", p.AccountID)
			continue
		}
		if state.Account(p.AccountID) == nil || p.WindowID == "" {
			report.skip("a daily point of unknown account %q", p.AccountID)
			continue
		}
		k := key{p.AccountID, p.WindowID}
		point := history.DailyPoint{Day: day, Used: p.Used}
		if p.DayStartUsed != nil {
			point.DayStartUsed = *p.DayStartUsed
			if hasBaseline[k] == nil {
				hasBaseline[k] = map[time.Time]bool{}
			}
			hasBaseline[k][day] = true
		}
		daily[k] = append(daily[k], point)
	}
	for _, raw := range src.History.RecentSamples {
		var s electronSample
		if err := json.Unmarshal(raw, &s); err != nil || s.Timestamp.IsZero() || s.Used < 0 {
			report.skip("a recent sample is malformed")
			continue
		}
		if state.Account(s.AccountID) == nil || s.WindowID == "" {
			report.skip("a recent sample of unknown account %q", s.AccountID)
			continue
		}
		k := key{s.AccountID, s.WindowID}
		samples[k] = append(samples[k], history.Reading{At: s.Timestamp, Used: s.Used})
	}
	for k, points := range daily {
		slices.SortStableFunc(points, func(a, b history.DailyPoint) int { return a.Day.Compare(b.Day) })
		// One point per day, the last one stored; a point without its own
		// baseline starts from the previous day's value, as Electron's budget
		// does, the first one from its own.
		unique := points[:0]
		for _, p := range points {
			if n := len(unique); n > 0 && unique[n-1].Day.Equal(p.Day) {
				unique[n-1] = p
				continue
			}
			unique = append(unique, p)
		}
		for i := range unique {
			if hasBaseline[k][unique[i].Day] {
				continue
			}
			unique[i].DayStartUsed = unique[i].Used
			if i > 0 {
				unique[i].DayStartUsed = unique[i-1].Used
			}
		}
		h := state.Account(k.account).History[k.window]
		h.DailyUsage = unique
		state.Account(k.account).History[k.window] = h
	}
	for k, readings := range samples {
		slices.SortStableFunc(readings, func(a, b history.Reading) int { return a.At.Compare(b.At) })
		h := state.Account(k.account).History[k.window]
		h.RecentSamples = slices.CompactFunc(readings, func(a, b history.Reading) bool { return a.At.Equal(b.At) })
		state.Account(k.account).History[k.window] = h
	}
	// A window with only one of the two lists still needs both non-nil.
	for i := range state.Accounts {
		for id, h := range state.Accounts[i].History {
			if h.DailyUsage == nil {
				h.DailyUsage = []history.DailyPoint{}
			}
			if h.RecentSamples == nil {
				h.RecentSamples = []history.Reading{}
			}
			state.Accounts[i].History[id] = h
		}
	}
}
