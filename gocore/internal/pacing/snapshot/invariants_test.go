package snapshot_test

import (
	"fmt"
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

const tolerance = 1e-9

// sweep builds S over a grid of used values, moments of the period and
// schedules, with a baseline 3 pp below x and work from 07:00, and calls check
// on every window snapshot: the invariants of §6 are properties of every input.
func sweep(t *testing.T, check func(t *testing.T, in snapshot.Input, w snapshot.WindowSnapshot)) {
	t.Helper()
	schedules := map[string]quotaperiod.Schedule{"weekdays": weekdays(), "disabled": weekdays(), "half-friday": weekdays()}
	schedules["disabled"] = quotaperiod.Schedule{Days: weekdays().Days, HoursPerDay: 8}
	half := weekdays()
	half.Days[time.Friday] = 0.5
	schedules["half-friday"] = half
	for name, schedule := range schedules {
		for _, kind := range []quotaperiod.Kind{quotaperiod.BillingCycle, quotaperiod.RollingDays} {
			for day := 1; day <= 31; day++ {
				for _, hour := range []int{0, 9, 13, 17, 23} {
					for _, x := range []float64{0, 0.05, 10, 40, 60, 80, 95, 99.9, 100, 103} {
						now := at(t, time.October, day, hour, 30)
						in := scenario(t, now, x)
						if kind == quotaperiod.RollingDays {
							// A weekly window around now, crossing the DST change late in the month.
							in.Response.Windows[0] = snapshot.Window{ID: "weekly", Kind: kind, Length: 7, Unit: pace.Percentage, Used: x,
								ResetsAt: now.Add(74 * time.Hour)}
						}
						in.Settings.Schedule = schedule
						in.Settings.TipDraw = 0.99
						if hour > 7 {
							today(t, &in, math.Max(0, x-3), at(t, time.October, day, 7, 0))
						}
						w := build(t, in)
						t.Run(fmt.Sprintf("%s %s %s x=%v", name, kind, now.Format("01-02 15:04"), x), func(t *testing.T) {
							t.Parallel()
							check(t, in, w)
						})
					}
				}
			}
		}
	}
}

func TestInvariants(t *testing.T) {
	t.Parallel()
	t.Run("I-1 I-2 target capped by the ceiling and the even hourly share", func(t *testing.T) {
		t.Parallel()
		sweep(t, func(t *testing.T, in snapshot.Input, w snapshot.WindowSnapshot) {
			t.Helper()
			if !w.HasTarget {
				return
			}
			sh := 100 / w.Units.Total / in.Settings.Schedule.HoursPerDay
			if w.Target > w.Ceiling+tolerance {
				t.Errorf("I-1: T %v > C %v", w.Target, w.Ceiling)
			}
			if w.Target > sh+tolerance {
				t.Errorf("I-2: T %v > s_h %v", w.Target, sh)
			}
			// Hours left only change C: T is C or s_h, never another value.
			if math.Abs(w.Target-math.Min(w.Ceiling, sh)) > tolerance {
				t.Errorf("I-2: T %v is neither C %v nor s_h %v", w.Target, w.Ceiling, sh)
			}
		})
	})
	t.Run("I-3 a tip never asks for 100 % or less than 1 %, none on an exhausted quota", func(t *testing.T) {
		t.Parallel()
		sweep(t, func(t *testing.T, _ snapshot.Input, w snapshot.WindowSnapshot) {
			t.Helper()
			if w.Used >= 100 && w.Tip != nil {
				t.Errorf("tip %+v at x = %v", w.Tip, w.Used)
			}
			if w.Tip != nil && w.Tip.Key == advice.TipAutonomy {
				if shown := math.Round(w.Tip.Params["reduction"]); shown < 1 || shown > 99 {
					t.Errorf("autonomy tip with reduction %v", w.Tip.Params["reduction"])
				}
			}
		})
	})
	t.Run("I-4 the verdict ratio is the exact one", func(t *testing.T) {
		t.Parallel()
		// Just under the 0.95 band once exact: q / s = (100 - x) * 22 / 900.
		x := 100 - 0.9499*900/22
		in := scenario(t, at(t, time.October, 20, 10, 0), x)
		today(t, &in, x-1, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		exact(t, "rho", w.Redistribution.Ratio(), (100-x)/9/(100.0/22))
		verdict(t, w, outlook.Behind)
	})
	t.Run("I-5 a missing value is hidden, not 0", func(t *testing.T) {
		t.Parallel()
		w := build(t, scenario(t, at(t, time.October, 20, 10, 0), 60))
		if w.Rating != nil {
			t.Errorf("rating %+v without history", w.Rating)
		}
		absent(t, "v", w.HasInstantPace)
		absent(t, "streak", w.Stats.HasStreak)
		absent(t, "peak/average", w.Stats.HasPeakAverage)
	})
	t.Run("I-6 nothing forward-looking without a period start or a reset ahead", func(t *testing.T) {
		t.Parallel()
		now := at(t, time.October, 20, 10, 0)
		// x = 80 would ask for nearReset if a passed reset still counted (D5).
		for name, window := range map[string]snapshot.Window{
			"unknown start": {ID: "w", Kind: quotaperiod.BillingCycle, Unit: pace.Percentage, Used: 80, ResetsAt: at(t, time.October, 27, 0, 0)},
			"missing reset": {ID: "w", Kind: quotaperiod.RollingDays, Length: 7, Unit: pace.Percentage, Used: 80},
			"reset passed":  {ID: "w", Kind: quotaperiod.RollingDays, Length: 7, Unit: pace.Percentage, Used: 80, ResetsAt: now.Add(-time.Hour)},
			"cycle over":    {ID: "w", Kind: quotaperiod.BillingCycle, Length: 1, Unit: pace.Percentage, Used: 80, ResetsAt: at(t, time.October, 20, 0, 0)},
		} {
			for _, fresh := range []bool{true, false} {
				in := scenario(t, now, 80)
				in.Response.Windows[0] = window
				in.Response.Fresh = fresh
				in.History["w"] = history.State{RecentSamples: []history.Reading{{At: now.Add(-time.Hour), Used: 79}}}
				w := build(t, in)
				noForwardLooking(t, w)
				if w.Hourly != nil || w.Tip != nil {
					t.Errorf("%s (fresh %v): hourly %+v, tip %+v", name, fresh, w.Hourly, w.Tip)
				}
				if name == "reset passed" || name == "cycle over" {
					absent(t, name+": days to reset", w.HasDaysToReset)
				}
				verdict(t, w, outlook.NoPacing)
			}
		}
	})
	t.Run("I-7 today is never judged as a completed day", func(t *testing.T) {
		t.Parallel()
		loc := rome(t)
		in := scenario(t, at(t, time.October, 20, 10, 0), 90)
		in.History["monthly"] = history.State{DailyUsage: []history.DailyPoint{
			{Day: startOfDay(at(t, time.October, 15, 0, 0), loc), Used: 42, DayStartUsed: 40},
			{Day: startOfDay(at(t, time.October, 16, 0, 0), loc), Used: 45, DayStartUsed: 42},
		}}
		today(t, &in, 45, at(t, time.October, 20, 7, 0)) // 45 pp today
		w := build(t, in)
		exact(t, "peak", w.Stats.Peak, 3)
		if w.Stats.Days != 2 {
			t.Errorf("completed days = %d, want 2", w.Stats.Days)
		}
		if w.Rating == nil || w.Rating.Average != (100.0/22/2+100.0/22/3)/2 {
			t.Errorf("rating = %+v, want the two completed days only", w.Rating)
		}
	})
	t.Run("I-8 units add up", func(t *testing.T) {
		t.Parallel()
		sweep(t, func(t *testing.T, in snapshot.Input, w snapshot.WindowSnapshot) {
			t.Helper()
			u := w.Units
			if u == nil {
				t.Fatal("no units")
			}
			dayUnit := in.Settings.Schedule.DayUnit(in.Now, in.Settings.Location)
			if math.Abs(u.Elapsed+u.Remaining-u.Total) > tolerance || u.TodayElapsed < 0 || u.TodayElapsed > dayUnit+tolerance || u.FromToday+tolerance < u.Remaining {
				t.Errorf("units %+v", *u)
			}
		})
	})
	t.Run("I-8 rolling days on the 25-hour DST day", func(t *testing.T) {
		t.Parallel()
		for _, hour := range []int{0, 9, 13, 17, 23} {
			for _, enabled := range []bool{true, false} {
				now := at(t, time.October, 25, hour, 30)
				in := scenario(t, now, 40)
				in.Settings.Schedule.Enabled = enabled
				in.Response.Windows[0] = snapshot.Window{ID: "weekly", Kind: quotaperiod.RollingDays, Length: 7, Unit: pace.Percentage, Used: 40,
					ResetsAt: at(t, time.October, 28, 12, 0)}
				w := build(t, in)
				u := w.Units
				if u == nil {
					t.Fatal("no units")
				}
				dayUnit := in.Settings.Schedule.DayUnit(now, in.Settings.Location)
				if math.Abs(u.Elapsed+u.Remaining-u.Total) > tolerance || u.TodayElapsed < 0 || u.TodayElapsed > dayUnit+tolerance || u.FromToday+tolerance < u.Remaining {
					t.Errorf("%s schedule %v: units %+v", now.Format("01-02 15:04"), enabled, *u)
				}
			}
		}
	})
	t.Run("I-9 the bar colour and the streak are the same flag", func(t *testing.T) {
		t.Parallel()
		loc := rome(t)
		in := scenario(t, at(t, time.October, 20, 10, 0), 61)
		in.History["monthly"] = history.State{DailyUsage: []history.DailyPoint{
			{Day: startOfDay(at(t, time.October, 15, 0, 0), loc), Used: 58, DayStartUsed: 48},
			{Day: startOfDay(at(t, time.October, 16, 0, 0), loc), Used: 59, DayStartUsed: 58},
			{Day: startOfDay(at(t, time.October, 19, 0, 0), loc), Used: 60, DayStartUsed: 59},
		}}
		w := build(t, in)
		if !slotOn(t, w, startOfDay(at(t, time.October, 15, 0, 0), loc)).OverBudget {
			t.Error("Thursday (10 pp) not over budget")
		}
		if !w.Stats.HasStreak || w.Stats.Streak != 2 {
			t.Errorf("streak = %+v, want 2 (broken on the red bar)", w.Stats)
		}
	})
	t.Run("I-10 P-7 projection uncapped, remaining never negative, used not clamped", func(t *testing.T) {
		t.Parallel()
		in := scenario(t, at(t, time.October, 20, 10, 0), 103)
		today(t, &in, 100, at(t, time.October, 20, 7, 0))
		w := build(t, in)
		exact(t, "x", w.Used, 103)
		exact(t, "rem", w.Remaining, 0)
		shown(t, "p", w.Summary.Pace, w.Summary.HasPace, 7.70, 2)
		shown(t, "proj", w.Summary.Projection, true, 169.4, 1)
		verdict(t, w, outlook.Exhausted)
		sweep(t, func(t *testing.T, _ snapshot.Input, w snapshot.WindowSnapshot) {
			t.Helper()
			if w.Remaining < 0 || w.Summary != nil && w.Summary.HasPace && w.Summary.Projection < w.Used-tolerance {
				t.Errorf("rem %v, projection %+v", w.Remaining, w.Summary)
			}
		})
	})
	t.Run("I-11 today is the local day", func(t *testing.T) {
		t.Parallel()
		loc := rome(t)
		// 00:30 in Rome is still the previous day in UTC.
		now := at(t, time.October, 21, 0, 30)
		in := scenario(t, now, 61)
		in.History["monthly"] = history.State{DailyUsage: []history.DailyPoint{
			{Day: startOfDay(at(t, time.October, 20, 0, 0), loc), Used: 60, DayStartUsed: 55},
		}}
		res := snapshot.Build(in)
		w := res.Account.Windows[0]
		exact(t, "baseline from the local previous day", w.Baseline, 60)
		exact(t, "used today", w.UsedToday, 1)
		points := res.History["monthly"].DailyUsage
		if len(points) != 2 || !points[1].Day.Equal(startOfDay(now, loc)) || points[0].Used != 60 {
			t.Errorf("points = %+v", points)
		}
	})
	t.Run("I-12 history has one writer", func(t *testing.T) {
		t.Parallel()
		loc := rome(t)
		now := at(t, time.October, 20, 10, 0)
		stored := history.State{
			DailyUsage:    []history.DailyPoint{{Day: startOfDay(at(t, time.October, 19, 0, 0), loc), Used: 55, DayStartUsed: 50}},
			RecentSamples: []history.Reading{{At: now.Add(-time.Hour), Used: 56}},
		}
		other := history.State{RecentSamples: []history.Reading{{At: now.Add(-time.Hour), Used: 1}}}
		in := scenario(t, now, 57)
		in.History["monthly"], in.History["gone"] = stored, other
		before := cloneHistory(in.History)

		res := snapshot.Build(in)
		if !reflect.DeepEqual(in.History, before) {
			t.Error("the input history was modified")
		}
		want := history.Record(stored, history.Sample{At: now, Used: 57, Fresh: true}, now, history.Settings{
			Location: loc, RetentionDays: 90, Period: history.PeriodInput{
				Start: at(t, time.October, 1, 0, 0), PreviousAt: startOfDay(now, loc).Add(-time.Nanosecond), PreviousUsed: 55,
			},
		})
		if !reflect.DeepEqual(res.History["monthly"], want) {
			t.Errorf("history = %+v, want history.Record's %+v", res.History["monthly"], want)
		}
		if !reflect.DeepEqual(res.History["gone"], other) {
			t.Error("a window missing from the response lost its history")
		}

		// A read just before midnight, built after it, on the manual renewal
		// day: the point belongs to the read's day and its period, as the
		// refresh loop records it (period and previous day at the fetch time).
		read := at(t, time.October, 20, 23, 50)
		late := snapshot.Input{
			History: map[string]history.State{"spend": {DailyUsage: []history.DailyPoint{
				{Day: startOfDay(at(t, time.October, 19, 0, 0), loc), Used: 25, DayStartUsed: 20},
			}}},
			Response: snapshot.Response{Windows: []snapshot.Window{{ID: "spend", Kind: quotaperiod.BillingCycle, Length: 1, Unit: pace.Percentage, Used: 30}},
				FetchedAt: read, Fresh: true},
			Settings: snapshot.Settings{Location: loc, Schedule: weekdays(), ChartDays: 7, Renewal: quotaperiod.RenewalRule{Type: "dayOfMonth", Day: 21}},
			Now:      at(t, time.October, 21, 0, 10),
		}
		wantLate := history.Record(late.History["spend"], history.Sample{At: read, Used: 30, Fresh: true}, read, history.Settings{
			Location: loc, RetentionDays: 90, Period: history.PeriodInput{
				Start:      at(t, time.September, 21, 0, 0),
				PreviousAt: startOfDay(read, loc).Add(-time.Nanosecond), PreviousUsed: 25,
			},
		})
		if got := snapshot.Build(late).History["spend"]; !reflect.DeepEqual(got, wantLate) {
			t.Errorf("late read history = %+v, want %+v", got, wantLate)
		}

		// Stale values are never recorded (D4); the same refresh twice records once.
		in.Response.Fresh = false
		if res := snapshot.Build(in); !reflect.DeepEqual(res.History["monthly"], stored) {
			t.Errorf("stale read recorded: %+v", res.History["monthly"])
		}
		in.Response.Fresh = true
		in.History = snapshot.Build(in).History
		again := snapshot.Build(in)
		if !reflect.DeepEqual(again.History, in.History) {
			t.Errorf("the same read recorded twice: %+v", again.History["monthly"])
		}
	})
}

func cloneHistory(h map[string]history.State) map[string]history.State {
	out := make(map[string]history.State, len(h))
	for id, state := range h {
		out[id] = history.State{
			DailyUsage:    append([]history.DailyPoint(nil), state.DailyUsage...),
			RecentSamples: append([]history.Reading(nil), state.RecentSamples...),
		}
	}
	return out
}
