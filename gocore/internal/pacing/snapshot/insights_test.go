package snapshot_test

import (
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/advice"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/insights"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/pace"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/snapshot"
)

// withY4 is S read at 15 % with the five working days before today consuming
// 1..5 pp (Y-4 of docs/PACING.md §3.15), and the local tokens of those days:
// 1000 output tokens each, 20 % of them above 150k context on the three light
// days and 60 % on the two heavy ones.
func withY4(t *testing.T) snapshot.Input {
	t.Helper()
	now := at(t, time.October, 20, 10, 0)
	in := scenario(t, now, 15)
	days := []int{13, 14, 15, 16, 19}
	state := history.State{}
	used := 0.0
	for i, d := range days {
		delta := float64(i + 1)
		state.DailyUsage = append(state.DailyUsage, history.DailyPoint{
			Day: at(t, time.October, d, 0, 0), DayStartUsed: used, Used: used + delta,
		})
		used += delta
		high := int64(200)
		if i >= 3 {
			high = 600
		}
		in.Settings.LocalTokens = append(in.Settings.LocalTokens, insights.Tokens{
			Date: at(t, time.October, d, 0, 0).Format(time.DateOnly), OutputTokens: 1000, HighContextOutputTokens: high,
		})
	}
	in.History["monthly"] = state
	return in
}

func TestLocalInsights(t *testing.T) {
	t.Parallel()
	t.Run("Y-4 and D-3.26 on a day-based window: yield and cause from its daily deltas", func(t *testing.T) {
		t.Parallel()
		w := build(t, withY4(t))
		if w.Yield == nil {
			t.Fatal("yield undefined, want 5000 tokens / 15 pp")
		}
		// 5000 / 15 = 333.3; trend: last two days 2000/9 over first two 2000/3, -66.7 %.
		if got := *w.Yield; got.TokensPerPercent != 333 || !got.TrendKnown || got.TrendPercent != -66.7 || got.DaysCompared != 5 {
			t.Errorf("yield = %+v, want 333 tokens/pp, trend -66.7 %%, 5 days", got)
		}
		if w.Cause == nil {
			t.Fatal("cause undefined, want (60, 20, 5)")
		}
		if got := *w.Cause; got != (insights.Cause{HighDaysHighContextPercent: 60, LowDaysHighContextPercent: 20, DaysCompared: 5}) {
			t.Errorf("cause = %+v, want (60, 20, 5)", got)
		}
	})

	t.Run("the cause is a tip candidate (§3.14)", func(t *testing.T) {
		t.Parallel()
		in := withY4(t)
		for draw := 0.0; draw < 1; draw += 0.05 {
			in.Settings.TipDraw = draw
			if tip := build(t, in).Tip; tip != nil && tip.Key == advice.TipCause {
				if tip.Params["highPercent"] != 60 || tip.Params["lowPercent"] != 20 || tip.Params["days"] != 5 {
					t.Errorf("cause tip params = %v, want 60, 20, 5", tip.Params)
				}
				return
			}
		}
		t.Error("no draw picks the cause tip")
	})

	t.Run("without local insights nothing is stated (opt-in)", func(t *testing.T) {
		t.Parallel()
		in := withY4(t)
		in.Settings.LocalTokens = nil
		w := build(t, in)
		if w.Yield != nil || w.Cause != nil {
			t.Errorf("yield %+v / cause %+v, want none", w.Yield, w.Cause)
		}
	})

	t.Run("Y-6 below the thresholds: absent, not zero", func(t *testing.T) {
		t.Parallel()
		in := withY4(t)
		in.Settings.LocalTokens = in.Settings.LocalTokens[:2]
		w := build(t, in)
		if w.Yield != nil || w.Cause != nil {
			t.Errorf("yield %+v / cause %+v, want none below 3 and 5 days", w.Yield, w.Cause)
		}
	})

	t.Run("only the chart's span plus one day of history, as Electron's analysis window", func(t *testing.T) {
		t.Parallel()
		in := withY4(t)
		// Four older points: with the five of Y-4, nine; the last eight are kept,
		// so the 6th, the only older one with tokens, is not compared.
		state := in.History["monthly"]
		var older []history.DailyPoint
		for _, d := range []int{6, 7, 8, 9} {
			older = append(older, history.DailyPoint{Day: at(t, time.October, d, 0, 0), DayStartUsed: 0, Used: 1})
		}
		state.DailyUsage = append(older, state.DailyUsage...)
		in.History["monthly"] = state
		in.Settings.LocalTokens = append(in.Settings.LocalTokens, insights.Tokens{Date: "2026-10-06", OutputTokens: 1000})
		w := build(t, in)
		if w.Yield == nil || w.Yield.DaysCompared != 5 {
			t.Errorf("yield = %+v, want 5 days compared (the 6th is outside the span)", w.Yield)
		}
	})

	t.Run("a rolling-hours window has no daily deltas, so no insights", func(t *testing.T) {
		t.Parallel()
		in := withY4(t)
		now := in.Now
		in.Response.Windows = []snapshot.Window{{
			ID: "five_hour", Kind: quotaperiod.RollingHours, Length: 5, Unit: pace.Percentage, Used: 15, ResetsAt: now.Add(2 * time.Hour),
		}}
		in.History = map[string]history.State{"five_hour": in.History["monthly"]}
		w := build(t, in)
		if w.Yield != nil || w.Cause != nil {
			t.Errorf("yield %+v / cause %+v, want none on rolling hours", w.Yield, w.Cause)
		}
	})
}
