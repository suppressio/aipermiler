package snapshot_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/advice"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/outlook"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/pace"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/snapshot"
)

// Scenario S of docs/PACING.md §0: Monday-Friday, 8 h days, billing cycle
// [2026-10-01, 2026-11-01), now Tuesday 2026-10-20 10:00 local.

func rome(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(t *testing.T, month time.Month, day, hour, minute int) time.Time {
	t.Helper()
	return time.Date(2026, month, day, hour, minute, 0, 0, rome(t))
}

func weekdays() quotaperiod.Schedule {
	return quotaperiod.Schedule{Enabled: true, Days: [7]float64{0, 1, 1, 1, 1, 1, 0}, HoursPerDay: 8}
}

func billingS(t *testing.T, x float64) snapshot.Window {
	t.Helper()
	return snapshot.Window{
		ID: "monthly", Kind: quotaperiod.BillingCycle, Length: 1, Unit: pace.Percentage, Used: x,
		ResetsAt: at(t, time.November, 1, 0, 0),
	}
}

// scenario is S with x read fresh at now and no history.
func scenario(t *testing.T, now time.Time, x float64) snapshot.Input {
	t.Helper()
	return snapshot.Input{
		History:  map[string]history.State{},
		Response: snapshot.Response{Windows: []snapshot.Window{billingS(t, x)}, FetchedAt: now, Fresh: true},
		Settings: snapshot.Settings{Location: rome(t), Schedule: weekdays(), ChartDays: 7},
		Now:      now,
	}
}

// today gives the first window today's point with baseline x0 and, when
// workedFrom is not zero, a sample at x0 at that time: a rise read at now then
// spans workedFrom to now.
func today(t *testing.T, in *snapshot.Input, x0 float64, workedFrom time.Time) {
	t.Helper()
	id := in.Response.Windows[0].ID
	state := in.History[id]
	day := startOfDay(in.Now, in.Settings.Location)
	state.DailyUsage = append(state.DailyUsage, history.DailyPoint{Day: day, Used: x0, DayStartUsed: x0})
	if !workedFrom.IsZero() {
		state.RecentSamples = append(state.RecentSamples, history.Reading{At: workedFrom, Used: x0})
	}
	in.History[id] = state
}

func startOfDay(at time.Time, loc *time.Location) time.Time {
	y, m, d := at.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

func build(t *testing.T, in snapshot.Input) snapshot.WindowSnapshot {
	t.Helper()
	res := snapshot.Build(in)
	if len(res.Account.Windows) == 0 {
		t.Fatal("no window in the snapshot")
	}
	return res.Account.Windows[0]
}

// shown checks the value as the documents show it, rounded to decimals: the
// rounding happens here, at display, never in the snapshot (I-4).
func shown(t *testing.T, name string, got float64, has bool, want float64, decimals int) {
	t.Helper()
	if !has {
		t.Errorf("%s undefined, want %.*f", name, decimals, want)
		return
	}
	scale := math.Pow(10, float64(decimals))
	if math.Round(got*scale)/scale != want {
		t.Errorf("%s = %.17g (shown %.*f), want %.*f", name, got, decimals, got, decimals, want)
	}
}

func exact(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %.17g, want %.17g", name, got, want)
	}
}

func absent(t *testing.T, name string, has bool) {
	t.Helper()
	if has {
		t.Errorf("%s is defined, want hidden (I-5)", name)
	}
}

func verdict(t *testing.T, w snapshot.WindowSnapshot, want outlook.Verdict) {
	t.Helper()
	if w.Judgement.Verdict != want {
		t.Errorf("verdict = %s (%s), want %s", w.Judgement.Verdict, w.Judgement.Reason, want)
	}
}

func slotOn(t *testing.T, w snapshot.WindowSnapshot, day time.Time) advice.Slot {
	t.Helper()
	for _, slot := range w.Chart {
		if slot.Day.Equal(day) {
			return slot
		}
	}
	t.Fatalf("no chart slot on %s", day.Format(time.DateOnly))
	return advice.Slot{}
}

func noForwardLooking(t *testing.T, w snapshot.WindowSnapshot) {
	t.Helper()
	if w.Summary != nil || w.Redistribution != nil {
		t.Errorf("summary %+v / redistribution %+v, want none (I-6)", w.Summary, w.Redistribution)
	}
	absent(t, "B", w.HasTodayBudget)
	absent(t, "T", w.HasTarget)
	absent(t, "C", w.HasCeiling)
}

// Edge cases of docs/PACING.md §5, end to end.

func TestEdgeCases(t *testing.T) {
	t.Parallel()
	t.Run("E1 start of window", func(t *testing.T) {
		t.Parallel()
		w := build(t, scenario(t, at(t, time.October, 1, 9, 0), 0))
		if w.Units == nil {
			t.Fatal("no units")
		}
		exact(t, "E", w.Units.Elapsed, 0)
		exact(t, "R", w.Units.Remaining, 22)
		absent(t, "idx", w.HasEfficiency)
		exact(t, "proj", w.Summary.Projection, 0)
		absent(t, "a", w.Summary.HasAutonomy)
		shown(t, "B", w.TodayBudget, w.HasTodayBudget, 4.55, 2)
		exact(t, "used today", w.UsedToday, 0)
		shown(t, "q", w.Redistribution.PerUnit, true, 4.55, 2)
		exact(t, "rho", w.Redistribution.Ratio(), 1)
		verdict(t, w, outlook.OnTrack)
		if !w.Summary.Preliminary {
			t.Error("not preliminary")
		}
		shown(t, "T", w.Target, w.HasTarget, 0.57, 2)
		absent(t, "v", w.HasInstantPace)
	})
	t.Run("E2 last minutes before the reset", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 30, 17, 0)
		in := scenario(t, now, 60)
		today(t, &in, 58, at(t, time.October, 30, 9, 6)) // 7.9 h worked
		w := build(t, in)
		exact(t, "R", w.Units.Remaining, 0.0125)
		shown(t, "proj", w.Summary.Projection, true, 60.0, 1)
		shown(t, "a", w.Summary.Autonomy, w.Summary.HasAutonomy, 14.7, 1)
		exact(t, "q", w.Redistribution.PerUnit, 40)
		shown(t, "rho", w.Redistribution.Ratio(), true, 8.8, 1)
		verdict(t, w, outlook.Ahead)
		exact(t, "B", w.TodayBudget, 42)
		exact(t, "C", w.Ceiling, 400)
		shown(t, "T", w.Target, w.HasTarget, 0.57, 2)
		if w.Tip != nil && w.Tip.Key == advice.TipAutonomy {
			t.Errorf("autonomy tip with a = %v > R", w.Summary.Autonomy)
		}
	})
	t.Run("E3 quota exhausted", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 100)
		today(t, &in, 90, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		verdict(t, w, outlook.Exhausted)
		exact(t, "a", w.Summary.Autonomy, 0)
		exact(t, "rem", w.Remaining, 0)
		exact(t, "q", w.Redistribution.PerUnit, 0)
		exact(t, "T", w.Target, 0)
		shown(t, "B", w.TodayBudget, w.HasTodayBudget, 1.11, 2)
		exact(t, "used today", w.UsedToday, 10)
		if !w.PaceAlert {
			t.Error("no pace alert")
		}
		if w.Tip != nil {
			t.Errorf("tip %+v on an exhausted quota (I-3, #73)", w.Tip)
		}
	})
	t.Run("E3b over the limit", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 103)
		today(t, &in, 90, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		verdict(t, w, outlook.Exhausted)
		exact(t, "x", w.Used, 103)
		exact(t, "rem", w.Remaining, 0)
		exact(t, "used today", w.UsedToday, 13)
		if w.Tip != nil {
			t.Errorf("tip %+v on an exhausted quota", w.Tip)
		}
	})
	t.Run("E4 no usage yet today", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 60)
		today(t, &in, 60, time.Time{})
		w := build(t, in)
		exact(t, "e", w.Units.TodayElapsed, 0)
		exact(t, "E", w.Units.Elapsed, 13)
		exact(t, "R", w.Units.Remaining, 9)
		exact(t, "used today", w.UsedToday, 0)
		shown(t, "B", w.TodayBudget, w.HasTodayBudget, 4.44, 2)
		absent(t, "v", w.HasInstantPace)
		exact(t, "gauge", w.Gauge, 0)
		if w.Rating != nil {
			t.Errorf("today rated: %+v", w.Rating)
		}
	})
	t.Run("E5 reset passed", func(t *testing.T) {
		t.Parallel()
		loc := rome(t)
		now := at(t, time.October, 20, 10, 0)
		stored := history.State{RecentSamples: []history.Reading{{At: at(t, time.October, 20, 8, 30), Used: 70}}}
		in := snapshot.Input{
			History: map[string]history.State{"five_hour": stored},
			Response: snapshot.Response{Windows: []snapshot.Window{{
				ID: "five_hour", Kind: quotaperiod.RollingHours, Length: 5, Unit: pace.Percentage, Used: 80,
				ResetsAt: at(t, time.October, 20, 9, 0),
			}}, FetchedAt: at(t, time.October, 20, 8, 45), Fresh: false},
			Settings: snapshot.Settings{Location: loc, Schedule: weekdays(), ChartDays: 7},
			Now:      now,
		}
		res := snapshot.Build(in)
		w := res.Account.Windows[0]
		if w.Hourly == nil {
			t.Fatal("no hourly outlook")
		}
		exact(t, "hoursLeft", w.Hourly.HoursLeft, 0)
		exact(t, "projected", w.Hourly.ProjectedAtReset, 80)
		absent(t, "autonomy", w.Hourly.HasAutonomy)
		absent(t, "T", w.HasTarget)
		verdict(t, w, outlook.NoPacing)
		if !reflect.DeepEqual(res.History["five_hour"], stored) {
			t.Errorf("stale values recorded: %+v", res.History["five_hour"])
		}
	})
	t.Run("E6 missing reset date", func(t *testing.T) {
		t.Parallel()
		for _, kind := range []quotaperiod.Kind{quotaperiod.RollingDays, quotaperiod.RollingHours} {
			in := scenario(t, at(t, time.October, 20, 10, 0), 50)
			in.Response.Windows[0] = snapshot.Window{ID: "weekly", Kind: kind, Length: 7, Unit: pace.Percentage, Used: 50}
			w := build(t, in)
			exact(t, "x", w.Used, 50)
			if w.Units != nil || w.Hourly != nil {
				t.Errorf("%s: units %+v, hourly %+v", kind, w.Units, w.Hourly)
			}
			noForwardLooking(t, w)
			verdict(t, w, outlook.NoPacing)
		}
	})
	t.Run("E7 schedule disabled", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 60)
		in.Settings.Schedule.Enabled = false
		today(t, &in, 57, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		shown(t, "idx", w.Efficiency, w.HasEfficiency, 1.04, 2)
		shown(t, "H", w.Units.RemainingHours, true, 92.67, 2)
		shown(t, "C", w.Ceiling, w.HasCeiling, 0.43, 2)
		shown(t, "T", w.Target, w.HasTarget, 0.40, 2)
	})
	t.Run("E8 non-working day", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 24, 10, 0)
		in := scenario(t, now, 70)
		today(t, &in, 70, time.Time{})
		w := build(t, in)
		exact(t, "e", w.Units.TodayElapsed, 0)
		absent(t, "B", w.HasTodayBudget)
		exact(t, "U", w.Redistribution.Units, 5)
		exact(t, "q", w.Redistribution.PerUnit, 6)
		slot := slotOn(t, w, startOfDay(now, rome(t)))
		if slot.DayUnit != 0 || slot.HasBudget || slot.OverBudget {
			t.Errorf("unused day off slot = %+v", slot)
		}
		// Any use on a day off is over its limit of 0.
		in = scenario(t, now, 70.5)
		today(t, &in, 70, time.Time{})
		if slot := slotOn(t, build(t, in), startOfDay(now, rome(t))); !slot.OverBudget {
			t.Errorf("used day off slot not over budget: %+v", slot)
		}
	})
	t.Run("E9 unknown period start", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 20, 10, 0)
		in := scenario(t, now, 72)
		in.Response.Windows[0] = snapshot.Window{ID: "credits", Kind: quotaperiod.BillingCycle, Unit: pace.Percentage, Used: 72,
			ResetsAt: at(t, time.October, 27, 0, 0)}
		in.History["credits"] = history.State{RecentSamples: []history.Reading{{At: at(t, time.October, 20, 9, 0), Used: 71}}}
		w := build(t, in)
		exact(t, "days to reset", w.DaysToReset, 7)
		absent(t, "idx", w.HasEfficiency)
		noForwardLooking(t, w)
		if w.Rating != nil {
			t.Errorf("rating %+v without pacing", w.Rating)
		}
		verdict(t, w, outlook.NoPacing)
		shown(t, "v", w.InstantPace, w.HasInstantPace, 1, 2)
		if w.Tip != nil {
			t.Errorf("tip %+v 7 days before the reset", w.Tip)
		}
		// Within 2 days: nearReset.
		in.Now, in.Response.FetchedAt = at(t, time.October, 25, 10, 0), at(t, time.October, 25, 10, 0)
		if w := build(t, in); w.Tip == nil || w.Tip.Key != advice.TipNearReset {
			t.Errorf("tip = %+v, want nearReset", w.Tip)
		}
	})
	t.Run("E10 half day", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 23, 10, 0), 52)
		in.Settings.Schedule.Days[time.Friday] = 0.5
		today(t, &in, 50, at(t, time.October, 23, 6, 0)) // 4 h worked
		w := build(t, in)
		exact(t, "e", w.Units.TodayElapsed, 0.5)
		exact(t, "R", w.Units.Remaining, w.Units.FromToday-0.5)
	})
	t.Run("E11 first day of history", func(t *testing.T) {
		t.Parallel()
		w := build(t, scenario(t, at(t, time.October, 20, 10, 0), 60))
		exact(t, "baseline (period started earlier)", w.Baseline, 60)
		exact(t, "used today", w.UsedToday, 0)
		w = build(t, scenario(t, at(t, time.October, 1, 9, 0), 2))
		exact(t, "baseline (period started today)", w.Baseline, 0)
		exact(t, "used today", w.UsedToday, 2)
	})
	t.Run("E12 reset during the day", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 20, 10, 0)
		in := scenario(t, now, 3.5)
		today(t, &in, 60, time.Time{})
		w := build(t, in)
		exact(t, "used today", w.UsedToday, 3.5)
		if slot := slotOn(t, w, startOfDay(now, rome(t))); slot.HasDelta {
			t.Errorf("delta of a reset day = %v, want none", slot.Delta)
		}
	})
	t.Run("E13 day without a refresh", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 21, 10, 0)
		in := scenario(t, now, 64)
		in.History["monthly"] = history.State{DailyUsage: []history.DailyPoint{
			{Day: startOfDay(at(t, time.October, 19, 0, 0), rome(t)), Used: 60, DayStartUsed: 55},
		}}
		w := build(t, in)
		exact(t, "Wed baseline", w.Baseline, 60)
		if slot := slotOn(t, w, startOfDay(now, rome(t))); !slot.HasDelta || slot.Delta != 4 {
			t.Errorf("Wed slot = %+v, want delta 4", slot)
		}
		if slot := slotOn(t, w, startOfDay(at(t, time.October, 20, 0, 0), rome(t))); slot.HasDelta {
			t.Errorf("Tue slot has a delta: %+v", slot)
		}
	})
	t.Run("E14 count window without total", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 30)
		in.Response.Windows = append([]snapshot.Window{{ID: "credits", Kind: quotaperiod.BillingCycle, Length: 1, Unit: pace.Count, Used: 12}},
			in.Response.Windows...)
		res := snapshot.Build(in)
		w := res.Account.Windows[0]
		absent(t, "utilization", w.HasUsed)
		verdict(t, w, outlook.NoPacing)
		if w.Units != nil || w.Summary != nil || w.HasTarget {
			t.Errorf("pacing without a utilization: %+v", w)
		}
		if _, ok := res.History["credits"]; ok {
			t.Error("history recorded without a utilization")
		}
		if res.Account.Critical != 1 {
			t.Errorf("critical = %d, want the window with a value", res.Account.Critical)
		}
	})
	t.Run("E14 days to reset and chart without a utilization", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 20, 10, 0)
		in := scenario(t, now, 30)
		in.Response.Windows = []snapshot.Window{{ID: "credits", Kind: quotaperiod.BillingCycle, Length: 1, Unit: pace.Count, Used: 12,
			ResetsAt: at(t, time.November, 1, 0, 0)}}
		w := snapshot.Build(in).Account.Windows[0]
		absent(t, "utilization", w.HasUsed)
		if !w.HasDaysToReset || w.DaysToReset <= 0 {
			t.Errorf("days to reset = %v (%v), want a positive value", w.DaysToReset, w.HasDaysToReset)
		}
		if !w.HasUnitsToReset || w.UnitsToReset <= 0 {
			t.Errorf("working units to reset = %v (%v), want a positive value", w.UnitsToReset, w.HasUnitsToReset)
		}
		if len(w.Chart) == 0 {
			t.Error("no chart without a utilization")
		}
		if w.Summary != nil || w.HasEfficiency || w.HasTarget {
			t.Errorf("percentage metrics without a utilization: %+v", w)
		}
	})
}

// Quantities composed end to end, by id.

func TestComposedQuantities(t *testing.T) {
	t.Parallel()
	t.Run("D-3.11 G-1 instant pace from the samples", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 20, 11, 0)
		in := scenario(t, now, 42.5)
		in.History["monthly"] = history.State{RecentSamples: []history.Reading{
			{At: at(t, time.October, 20, 10, 0), Used: 40}, {At: at(t, time.October, 20, 10, 30), Used: 41},
		}}
		w := build(t, in)
		exact(t, "v", w.InstantPace, 2.5)
		if !w.HasGauge {
			t.Fatal("no gauge with a target")
		}
		exact(t, "g", w.Gauge, pace.GaugePosition(2.5, w.Target))
	})
	// D-3.28: the TR-2 samples, the 11:00 one read by the snapshot itself.
	trendInput := func(t *testing.T, day int, fresh bool) snapshot.Input {
		t.Helper()
		now := at(t, time.October, day, 11, 0)
		in := scenario(t, now, 20)
		in.Response.Fresh = fresh
		in.History["monthly"] = history.State{RecentSamples: []history.Reading{
			{At: at(t, time.October, day, 9, 30), Used: 10}, {At: at(t, time.October, day, 10, 0), Used: 10},
			{At: at(t, time.October, day, 10, 30), Used: 11},
		}}
		return in
	}
	t.Run("D-3.28 TR-2 instant trend from the samples", func(t *testing.T) {
		t.Parallel()
		w := build(t, trendInput(t, 20, true))
		if !w.HasTrend || w.Trend != pace.Accelerating {
			t.Errorf("trend = %q, %v; want accelerating", w.Trend, w.HasTrend)
		}
	})
	t.Run("D-3.28 TR-8 no trend on a read that is not fresh", func(t *testing.T) {
		t.Parallel()
		in := trendInput(t, 20, false)
		// The last known value, read at 11:00 by an earlier refresh: the
		// samples alone would give a trend, the failed refresh hides it.
		state := in.History["monthly"]
		state.RecentSamples = append(state.RecentSamples, history.Reading{At: in.Now, Used: 20})
		in.History["monthly"] = state
		w := build(t, in)
		absent(t, "trend", w.HasTrend)
	})
	t.Run("D-3.28 TR-12 no day-off rule", func(t *testing.T) {
		t.Parallel()
		w := build(t, trendInput(t, 24, true))
		if !w.HasTrend || w.Trend != pace.Accelerating {
			t.Errorf("trend on a day off = %q, %v; want accelerating", w.Trend, w.HasTrend)
		}
	})
	t.Run("D-3.19 Q-1 ratio", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 60)
		today(t, &in, 57, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		shown(t, "q", w.Redistribution.PerUnit, true, 4.444, 3)
		shown(t, "s", w.Redistribution.EvenShare, true, 4.545, 3)
		shown(t, "rho", w.Redistribution.Ratio(), true, 0.978, 3)
		verdict(t, w, outlook.OnTrack)
	})
	t.Run("D-3.15 T-11 no budget before the period start", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 5)
		in.Response.Windows[0].ResetsAt = at(t, time.December, 1, 0, 0)
		today(t, &in, 4, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		exact(t, "E", w.Units.Elapsed, 0)
		exact(t, "R", w.Units.Remaining, w.Units.Total)
		absent(t, "B", w.HasTodayBudget)
	})
	t.Run("P-5 efficiency on almost nothing", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 0.05)
		today(t, &in, 0, at(t, time.October, 20, 7, 0))
		absent(t, "idx", build(t, in).HasEfficiency)
	})
	t.Run("P-6 efficiency index", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 60)
		today(t, &in, 57, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		exact(t, "E", w.Units.Elapsed, 13.375)
		shown(t, "idx", w.Efficiency, w.HasEfficiency, 1.01, 2)
	})
	t.Run("P-1 recent pace blended into projection and autonomy", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 60)
		loc := rome(t)
		in.History["monthly"] = history.State{DailyUsage: []history.DailyPoint{
			{Day: startOfDay(at(t, time.October, 15, 0, 0), loc), Used: 45, DayStartUsed: 40},
			{Day: startOfDay(at(t, time.October, 16, 0, 0), loc), Used: 50, DayStartUsed: 45},
			{Day: startOfDay(at(t, time.October, 19, 0, 0), loc), Used: 55, DayStartUsed: 50},
		}}
		today(t, &in, 57, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		shown(t, "p", w.Summary.Pace, w.Summary.HasPace, 4.743, 3)
		shown(t, "proj", w.Summary.Projection, true, 100.9, 1)
		shown(t, "a", w.Summary.Autonomy, w.Summary.HasAutonomy, 8.4, 1)
		shown(t, "working days to reset", w.Summary.WorkingUnitsToReset, true, 8.6, 1)
		verdict(t, w, outlook.OnTrack) // V-2
		if w.Tip == nil || w.Tip.Key != advice.TipAutonomy {
			t.Fatalf("tip = %+v, want autonomy (TP-1)", w.Tip)
		}
		shown(t, "reduction", w.Tip.Params["reduction"], true, 2, 0)
	})
	t.Run("G-10 D-2.7 hours window: calendar hours to the reset", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 20, 10, 0)
		in := scenario(t, now, 67)
		in.Response.Windows[0] = snapshot.Window{ID: "five_hour", Kind: quotaperiod.RollingHours, Length: 5, Unit: pace.Percentage, Used: 67,
			ResetsAt: now.Add(2 * time.Hour)}
		w := build(t, in)
		exact(t, "C", w.Ceiling, 16.5)
		exact(t, "T", w.Target, 16.5)
		verdict(t, w, outlook.OnTrack) // V-9: projected 67 without a pace
		if w.Chart != nil || w.Rating != nil || w.Units != nil {
			t.Error("day-based readings on a rolling-hours window")
		}
	})
	t.Run("V-12 the widget opens on the window at risk", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 20, 10, 0)
		in := scenario(t, now, 67)
		in.Settings.Schedule.Enabled = false
		in.Response.Windows = []snapshot.Window{
			{ID: "five_hour", Kind: quotaperiod.RollingHours, Length: 5, Unit: pace.Percentage, Used: 67, ResetsAt: now.Add(2 * time.Hour)},
			{ID: "weekly", Kind: quotaperiod.RollingDays, Length: 7, Unit: pace.Percentage, Used: 55, ResetsAt: now.Add(166 * time.Hour)},
		}
		res := snapshot.Build(in)
		verdict(t, res.Account.Windows[0], outlook.OnTrack)
		verdict(t, res.Account.Windows[1], outlook.AtRisk)
		if res.Account.Critical != 1 {
			t.Errorf("critical = %d, want the weekly window", res.Account.Critical)
		}
	})
}

// Regression cases of docs/PACING.md §8.

func TestRegressionCases(t *testing.T) {
	t.Parallel()
	t.Run("#64 B-2 B-3 B-4 used today at full precision", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			id        string
			x0, x     float64
			usedToday float64
		}{
			{"B-2", 57.04, 57.0449, 0.0049},
			{"B-3", 40, 39.6, 0},
			{"B-4", 40, 3.5, 3.5},
		} {
			in := scenario(t, at(t, time.October, 20, 10, 0), tc.x)
			today(t, &in, tc.x0, time.Time{})
			w := build(t, in)
			exact(t, tc.id+" used today", w.UsedToday, tc.usedToday)
		}
		in := scenario(t, at(t, time.October, 20, 10, 0), 57.0449)
		today(t, &in, 57.04, time.Time{})
		shown(t, "B-2 used today", build(t, in).UsedToday, true, 0.00, 2)
	})
	t.Run("#64 R-1 the rating tip states the rating's average", func(t *testing.T) {
		t.Parallel()
		loc := rome(t)
		in := scenario(t, at(t, time.October, 20, 10, 0), 36)
		in.History["monthly"] = history.State{DailyUsage: []history.DailyPoint{
			{Day: startOfDay(at(t, time.October, 15, 0, 0), loc), Used: 22, DayStartUsed: 20},
			{Day: startOfDay(at(t, time.October, 16, 0, 0), loc), Used: 26.55, DayStartUsed: 22},
			{Day: startOfDay(at(t, time.October, 19, 0, 0), loc), Used: 35.65, DayStartUsed: 26.55},
		}}
		today(t, &in, 35.65, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		if w.Rating == nil {
			t.Fatal("no rating")
		}
		if w.Rating.Stars != 4 {
			t.Errorf("stars = %d, want 4", w.Rating.Stars)
		}
		shown(t, "avg", w.Rating.Average, true, 1.26, 2)
		if w.Tip == nil || w.Tip.Key != advice.TipRating {
			t.Fatalf("tip = %+v, want rating", w.Tip)
		}
		if w.Tip.Params["average"] != w.Rating.Average {
			t.Errorf("tip avgRatio %v != rating avgRatio %v", w.Tip.Params["average"], w.Rating.Average)
		}
	})
	t.Run("#72 G-12 the target stays at the even share near the reset", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 30, 17, 0), 66.9)
		today(t, &in, 66, at(t, time.October, 30, 9, 6))
		w := build(t, in)
		shown(t, "H", w.Units.RemainingHours, true, 0.1, 2)
		shown(t, "C", w.Ceiling, w.HasCeiling, 331, 0)
		shown(t, "T", w.Target, w.HasTarget, 0.57, 2)
	})
	t.Run("#73 TP-2 TP-3 no tip on an exhausted or almost exhausted quota", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 100)
		today(t, &in, 90, at(t, time.October, 20, 7, 0))
		if w := build(t, in); w.Tip != nil {
			t.Errorf("TP-2: tip %+v at x = 100", w.Tip)
		}
		in = scenario(t, at(t, time.October, 20, 10, 0), 99.9)
		today(t, &in, 95, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		if !w.Summary.HasAutonomy || w.Summary.Autonomy >= w.Summary.WorkingUnitsToReset {
			t.Fatalf("a = %v, R = %v: not the TP-3 shape", w.Summary.Autonomy, w.Summary.WorkingUnitsToReset)
		}
		if w.Tip != nil {
			t.Errorf("TP-3: tip %+v asking for a reduction of about 100 %%", w.Tip)
		}
	})
	t.Run("HYP#78 T-15 rolling 7 days keep their exact bounds", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 17, 18, 38)
		in := scenario(t, now, 40)
		in.Settings.Schedule.Enabled = false
		in.Response.Windows[0] = snapshot.Window{ID: "weekly", Kind: quotaperiod.RollingDays, Length: 7, Unit: pace.Percentage, Used: 40,
			ResetsAt: at(t, time.October, 21, 23, 0)}
		w := build(t, in)
		exact(t, "N", w.Units.Total, 7)
		exact(t, "E", w.Units.Elapsed, 4058.0/1440)
		exact(t, "R", w.Units.Remaining, 6022.0/1440)
		shown(t, "E", w.Units.Elapsed, true, 2.82, 2)
		shown(t, "R", w.Units.Remaining, true, 4.18, 2)
	})
}

// E9: a billing cycle of unknown length has no start, so no summary, but its
// working units to the reset need only the reset and the schedule (HYP#348).
func TestUnitsToResetWithoutPeriodStart(t *testing.T) {
	t.Parallel()
	in := scenario(t, at(t, time.October, 21, 11, 0), 12)
	in.Response.Windows[0].Length = 0
	w := build(t, in)
	if w.Summary != nil || w.Units != nil {
		t.Fatalf("summary %+v / units %+v, want none without a period start", w.Summary, w.Units)
	}
	exact(t, "R", w.UnitsToReset, 8)
	if !w.HasUnitsToReset {
		t.Error("R undefined, want 8")
	}
}
