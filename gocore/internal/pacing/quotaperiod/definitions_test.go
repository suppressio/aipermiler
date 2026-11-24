package quotaperiod_test

import (
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
)

// The definitions of docs/PACING.md §2, one test each, on scenario S
// (billing cycle [2026-10-01, 2026-11-01), now Tuesday 2026-10-20 10:00).

func scenarioS(t *testing.T) (quotaperiod.Period, time.Time, *time.Location) {
	t.Helper()
	loc := location(t)
	return quotaperiod.Period{
		Kind:  quotaperiod.BillingCycle,
		Start: time.Date(2026, time.October, 1, 0, 0, 0, 0, loc),
		End:   time.Date(2026, time.November, 1, 0, 0, 0, 0, loc),
	}, time.Date(2026, time.October, 20, 10, 0, 0, 0, loc), loc
}

func units(t *testing.T, p quotaperiod.Period, now time.Time, loc *time.Location, s quotaperiod.Schedule, worked *float64) quotaperiod.WorkingUnits {
	t.Helper()
	got, ok := p.Units(now, loc, s, worked)
	if !ok {
		t.Fatal("units unavailable")
	}
	return got
}

func hours(h float64) *float64 { return &h }

func TestTimeDefinitions(t *testing.T) {
	t.Parallel()
	t.Run("D-2.1 day unit", func(t *testing.T) {
		t.Parallel()
		loc := location(t)
		tuesday := time.Date(2026, time.October, 20, 10, 0, 0, 0, loc)
		friday := time.Date(2026, time.October, 23, 10, 0, 0, 0, loc)
		saturday := time.Date(2026, time.October, 24, 10, 0, 0, 0, loc)
		closeTo(t, "full", schedule("").DayUnit(tuesday, loc), 1)
		closeTo(t, "half", schedule("half-friday").DayUnit(friday, loc), 0.5)
		closeTo(t, "off", schedule("").DayUnit(saturday, loc), 0)
		closeTo(t, "disabled", schedule("disabled").DayUnit(saturday, loc), 1)
	})
	t.Run("D-2.2 working units between", func(t *testing.T) {
		t.Parallel()
		p, _, loc := scenarioS(t)
		closeTo(t, "N", quotaperiod.WorkingUnitsBetween(p.Start, p.End, loc, schedule("")), 22)
		closeTo(t, "b <= a", quotaperiod.WorkingUnitsBetween(p.End, p.Start, loc, schedule("")), 0)
		// Rolling days weight the overlap of each local day (T-15).
		rolling := quotaperiod.Period{
			Kind:  quotaperiod.RollingDays,
			Start: time.Date(2026, time.October, 14, 23, 0, 0, 0, loc),
			End:   time.Date(2026, time.October, 21, 23, 0, 0, 0, loc),
		}
		closeTo(t, "N rolling", units(t, rolling, rolling.Start, loc, schedule(""), nil).Total, 5)
		closeTo(t, "N rolling disabled", units(t, rolling, rolling.Start, loc, schedule("disabled"), nil).Total, 7)
	})
	t.Run("D-2.3 today's elapsed units", func(t *testing.T) {
		t.Parallel()
		p, now, loc := scenarioS(t)
		closeTo(t, "no activity", units(t, p, now, loc, schedule(""), nil).TodayElapsed, 0)
		closeTo(t, "2 h floor", units(t, p, now, loc, schedule(""), hours(0.5)).TodayElapsed, 0.25)
		closeTo(t, "worked", units(t, p, now, loc, schedule(""), hours(3)).TodayElapsed, 0.375)
		closeTo(t, "capped", units(t, p, now, loc, schedule(""), hours(12)).TodayElapsed, 1)
		saturday := time.Date(2026, time.October, 24, 10, 0, 0, 0, loc)
		closeTo(t, "day off", units(t, p, saturday, loc, schedule(""), hours(3)).TodayElapsed, 0)
		closeTo(t, "disabled", units(t, p, now, loc, schedule("disabled"), nil).TodayElapsed, 10.0/24)
	})
	t.Run("D-2.4 elapsed units", func(t *testing.T) {
		t.Parallel()
		p, now, loc := scenarioS(t)
		got := units(t, p, now, loc, schedule(""), hours(3))
		closeTo(t, "E = W(start, today) + e", got.Elapsed, quotaperiod.WorkingUnitsBetween(p.Start, now, loc, schedule(""))+got.TodayElapsed)
		closeTo(t, "E", got.Elapsed, 13.375)
	})
	t.Run("D-2.5 remaining units", func(t *testing.T) {
		t.Parallel()
		p, now, loc := scenarioS(t)
		got := units(t, p, now, loc, schedule(""), hours(3))
		tomorrow := time.Date(2026, time.October, 21, 0, 0, 0, 0, loc)
		closeTo(t, "R = (u - e) + W(today + 1, end)", got.Remaining, 1-got.TodayElapsed+quotaperiod.WorkingUnitsBetween(tomorrow, p.End, loc, schedule("")))
		closeTo(t, "E + R = N", got.Elapsed+got.Remaining, got.Total)
	})
	t.Run("D-2.6 units from today", func(t *testing.T) {
		t.Parallel()
		p, now, loc := scenarioS(t)
		closeTo(t, "U", units(t, p, now, loc, schedule(""), hours(3)).FromToday, 9)
		closeTo(t, "U fixed during the day", units(t, p, now.Add(7*time.Hour), loc, schedule(""), hours(10)).FromToday, 9)
		// Rolling days: only today's part inside the window, fixed during the day (T-16).
		rolling := quotaperiod.Period{
			Kind:  quotaperiod.RollingDays,
			Start: time.Date(2026, time.October, 14, 23, 0, 0, 0, loc),
			End:   time.Date(2026, time.October, 21, 23, 0, 0, 0, loc),
		}
		friday := time.Date(2026, time.October, 16, 12, 0, 0, 0, loc)
		closeTo(t, "U rolling", units(t, rolling, friday, loc, schedule(""), nil).FromToday, 95.0/24)
		closeTo(t, "U rolling later", units(t, rolling, friday.Add(6*time.Hour), loc, schedule(""), nil).FromToday, 95.0/24)
	})
	t.Run("D-2.7 remaining working hours", func(t *testing.T) {
		t.Parallel()
		p, now, loc := scenarioS(t)
		got := units(t, p, now, loc, schedule(""), hours(3))
		closeTo(t, "H = R x hoursPerDay", got.RemainingHours, 69)
		disabled := units(t, p, now, loc, schedule("disabled"), nil)
		closeTo(t, "H with the schedule disabled", disabled.RemainingHours, disabled.Remaining*8)
		// A window read in hours uses the calendar hours to the reset instead.
		fiveHour := quotaperiod.Period{Kind: quotaperiod.RollingHours, Start: now.Add(-3 * time.Hour), End: now.Add(2 * time.Hour)}
		left, ok := fiveHour.HoursUntilReset(now)
		if !ok {
			t.Fatal("no reset")
		}
		closeTo(t, "calendar hours", left, 2)
	})
}
