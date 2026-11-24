package advice

import (
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/pace"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
)

// Slot is one day of the chart (D-3.23).
type Slot struct {
	Day time.Time
	// Delta is the consumption of a past day or today; HasDelta is false for
	// days to come, days without a point and reset days.
	Delta    float64
	HasDelta bool
	// FullShare is the even share s in pp, only inside the period.
	FullShare    float64
	HasFullShare bool
	DayUnit      float64
	// Budget is the moving budget: that morning's budget on a past day, today's
	// budget today, the redistributed quota ahead; none outside the period.
	Budget    float64
	HasBudget bool
	Upcoming  bool
	// OverBudget is Delta above the budget, or above the share times the day
	// unit where there is no budget; the same flag colours the bar and breaks
	// the streak (I-9).
	OverBudget bool
}

// ChartInput is what the chart needs; the period bounds are those of the window
// (Start zero or TotalUnits <= 0: no pacing, hence no share and no budget).
type ChartInput struct {
	Points       []history.DailyPoint
	Schedule     quotaperiod.Schedule
	Start, End   time.Time
	TotalUnits   float64
	Now          time.Time
	Location     *time.Location
	PastDays     int
	UpcomingDays int
	// TodayBudget is B, already computed for today (outlook.TodayBudget).
	TodayBudget    float64
	HasTodayBudget bool
	// PerUnit is q, the redistributed quota per working unit (outlook.Redistribute).
	PerUnit    float64
	HasPerUnit bool
}

// Chart returns the slots of the last PastDays days ending today followed by
// UpcomingDays days to come (D-3.23). The moving budget of a past day uses the
// rule of today's budget: what was left at the start of that day over the
// working units from that day to the reset, times the day's unit.
func Chart(in ChartInput) []Slot {
	loc := in.Location
	pacing := in.TotalUnits > 0 && !in.Start.IsZero() && !in.End.IsZero()
	today := startOfDay(in.Now, loc)
	firstDay, lastDay := startOfDay(in.Start, loc), startOfDay(in.End, loc)

	deltas := make(map[string]Delta)
	for _, d := range Deltas(in.Points, in.Schedule, in.TotalUnits, loc) {
		deltas[d.Day.Format(time.DateOnly)] = d
	}
	baselines := make(map[string]float64, len(in.Points))
	for _, point := range in.Points {
		baselines[point.Day.Format(time.DateOnly)] = point.DayStartUsed
	}

	slots := make([]Slot, 0, in.PastDays+in.UpcomingDays)
	for offset := 1 - in.PastDays; offset <= in.UpcomingDays; offset++ {
		day := today.AddDate(0, 0, offset)
		key := day.Format(time.DateOnly)
		slot := Slot{Day: day, DayUnit: in.Schedule.DayUnit(day, loc), Upcoming: offset > 0}
		inPeriod := pacing && !day.Before(firstDay) && day.Before(lastDay)
		if inPeriod {
			slot.FullShare, slot.HasFullShare = pace.EvenShare(in.TotalUnits)
			slot.Budget, slot.HasBudget = in.budget(day, offset, key, slot.DayUnit, baselines)
		}
		if offset <= 0 {
			if d, ok := deltas[key]; ok && d.HasDelta {
				slot.Delta, slot.HasDelta = d.Delta, true
			}
		}
		limit, hasLimit := slot.Budget, slot.HasBudget
		if !hasLimit && slot.HasFullShare {
			limit, hasLimit = slot.FullShare*slot.DayUnit, true
		}
		slot.OverBudget = slot.HasDelta && hasLimit && slot.Delta > limit
		slots = append(slots, slot)
	}
	return slots
}

func (in ChartInput) budget(day time.Time, offset int, key string, dayUnit float64, baselines map[string]float64) (float64, bool) {
	switch {
	case offset == 0:
		return in.TodayBudget, in.HasTodayBudget
	case offset > 0:
		return in.PerUnit * dayUnit, in.HasPerUnit
	}
	baseline, ok := baselines[key]
	if !ok {
		return 0, false
	}
	units := quotaperiod.WorkingUnitsBetween(day, in.End, in.Location, in.Schedule)
	if units <= 0 {
		return 0, false
	}
	return pace.Remaining(baseline) / units * dayUnit, true
}

// Stats are the peak, the average and the streak of the completed days of a chart.
type Stats struct {
	// Days is how many completed days have a delta.
	Days int
	// Peak and Average are over those days, days off included (D-3.24).
	Peak, Average float64
	// HasPeakAverage is false with fewer than 2 days: one day says nothing more
	// than its own value (C-8).
	HasPeakAverage bool
	// Streak counts back from the most recent completed day the days that did
	// not go over their limit (D-3.25); HasStreak is false without a limit to
	// compare with or without any consumption.
	Streak    int
	HasStreak bool
}

// Summarize computes the statistics on the chart slots, the same days and the
// same limit as the bars. Today and days to come are not judged (I-7).
func Summarize(chart []Slot, now time.Time, loc *time.Location) Stats {
	today := startOfDay(now, loc)
	var completed []Slot
	for _, slot := range chart {
		if !slot.Upcoming && slot.HasDelta && slot.Day.Before(today) {
			completed = append(completed, slot)
		}
	}
	stats := Stats{Days: len(completed)}
	if len(completed) == 0 {
		return stats
	}
	var sum float64
	hasLimit, consumed := false, false
	for _, slot := range completed {
		stats.Peak = max(stats.Peak, slot.Delta)
		sum += slot.Delta
		hasLimit = hasLimit || slot.HasBudget || slot.HasFullShare
		consumed = consumed || slot.Delta > 0
	}
	stats.Average = sum / float64(len(completed))
	stats.HasPeakAverage = len(completed) >= 2
	if !hasLimit || !consumed {
		return stats
	}
	stats.HasStreak = true
	for i := len(completed) - 1; i >= 0; i-- {
		slot := completed[i]
		if slot.DayUnit == 0 && slot.Delta == 0 {
			continue
		}
		if slot.OverBudget || (!slot.HasBudget && !slot.HasFullShare) {
			break
		}
		stats.Streak++
	}
	return stats
}
