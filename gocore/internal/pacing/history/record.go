// Package history records one account window's observations without I/O or a
// hidden clock. The caller owns persistence and serializes calls to Record.
package history

import "time"

// DailyPoint holds the last utilization of a local day and its fixed baseline.
// Day is the local midnight; quota values are percentage points at full precision.
type DailyPoint struct {
	Day          time.Time
	Used         float64
	DayStartUsed float64
}

// Reading is one timestamped utilization, including fresh readings with no rise.
type Reading struct {
	At   time.Time
	Used float64
}

// State belongs to a single account and quota window.
type State struct {
	DailyUsage    []DailyPoint
	RecentSamples []Reading
}

// Sample identifies an observation by its fetch timestamp, not its value.
// Fresh is false for cached values and responses retained after a fetch failure.
type Sample struct {
	At    time.Time
	Used  float64
	Fresh bool
}

// PeriodInput supplies period facts without depending on a period package.
// Start is zero when unknown. PreviousAt and PreviousUsed describe the last
// observation of an earlier local day, including gaps in history; a zero
// PreviousAt means no earlier point. A drop of more than resetThreshold pp
// against PreviousUsed is a reset; a smaller drop is provider noise.
type PeriodInput struct {
	Start        time.Time
	PreviousAt   time.Time
	PreviousUsed float64
}

// resetThreshold is the drop in percentage points above which a lower value is a
// reset rather than provider noise (PACING §7, D-3.16).
const resetThreshold = 1.0

// thresholdTolerance absorbs float64 rounding in the subtraction, so that a drop
// of exactly 1 pp (2.24 -> 1.24 gives 1.0000000000000002) stays noise.
const thresholdTolerance = 1e-9

// Settings provides an explicit local calendar, retention and period context.
// Location must be non-nil and RetentionDays positive. Retention counts local
// calendar dates including today, without filling days that had no observation.
type Settings struct {
	Location      *time.Location
	RetentionDays int
	Period        PeriodInput
}

// Record returns the next state, preserving full precision and never modifying
// the input slices. Observations must have finite, normalized quota values and
// fetch timestamps no later than now. Only fresh observations newer than every
// retained sample are accepted (the caller serializes calls with increasing
// fetch timestamps: daily points carry no timestamp of their own); ignored
// observations do not even prune state.
// A day's baseline is set at creation and never changed, including on a reset.
func Record(state State, sample Sample, now time.Time, settings Settings) State {
	if !sample.Fresh {
		return state
	}
	for _, previous := range state.RecentSamples {
		if !sample.At.After(previous.At) {
			return state
		}
	}

	today := startOfDay(now, settings.Location)
	day := startOfDay(sample.At, settings.Location)
	dailyCutoff := today.AddDate(0, 0, 1-settings.RetentionDays)
	sampleCutoff := now.Add(-4 * time.Hour)
	if today.Before(sampleCutoff) {
		sampleCutoff = today
	}

	next := State{
		DailyUsage:    make([]DailyPoint, 0, len(state.DailyUsage)+1),
		RecentSamples: make([]Reading, 0, len(state.RecentSamples)+1),
	}
	found := false
	for _, point := range state.DailyUsage {
		if point.Day.Equal(day) {
			point.Used = sample.Used
			found = true
		}
		if !point.Day.Before(dailyCutoff) {
			next.DailyUsage = append(next.DailyUsage, point)
		}
	}
	if !found && !day.Before(dailyCutoff) {
		next.DailyUsage = append(next.DailyUsage, DailyPoint{
			Day: day, Used: sample.Used, DayStartUsed: baseline(sample, day, settings),
		})
	}
	for _, previous := range state.RecentSamples {
		if !previous.At.Before(sampleCutoff) {
			next.RecentSamples = append(next.RecentSamples, previous)
		}
	}
	if !sample.At.Before(sampleCutoff) {
		next.RecentSamples = append(next.RecentSamples, Reading{At: sample.At, Used: sample.Used})
	}
	return next
}

func baseline(sample Sample, day time.Time, settings Settings) float64 {
	period := settings.Period
	if period.PreviousAt.IsZero() {
		if !period.Start.IsZero() && !period.Start.After(sample.At) &&
			startOfDay(period.Start, settings.Location).Equal(day) {
			return 0
		}
		return sample.Used
	}
	if period.PreviousAt.Before(period.Start) || period.PreviousUsed-sample.Used > resetThreshold+thresholdTolerance {
		return 0
	}
	return period.PreviousUsed
}

func startOfDay(at time.Time, location *time.Location) time.Time {
	local := at.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}
