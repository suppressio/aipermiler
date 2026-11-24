package advice

import (
	"slices"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
)

// Delta is the consumption of one local day next to that day's ideal share.
type Delta struct {
	Day time.Time
	// Delta is Used - DayStartUsed in pp; HasDelta is false when the window was
	// reset in between (negative, not attributable to that day's use, D-3.21).
	Delta    float64
	HasDelta bool
	// IdealShare is s * u(day) in pp; HasShare is false without pacing
	// (totalUnits <= 0). It is 0 on a day off.
	IdealShare float64
	HasShare   bool
}

// Deltas returns one entry per daily point, oldest first (D-3.21). totalUnits
// is N, the working units of the whole period; 0 or less means no pacing.
func Deltas(points []history.DailyPoint, schedule quotaperiod.Schedule, totalUnits float64, loc *time.Location) []Delta {
	sorted := slices.SortedStableFunc(slices.Values(points), func(a, b history.DailyPoint) int { return a.Day.Compare(b.Day) })
	result := make([]Delta, 0, len(sorted))
	for _, point := range sorted {
		entry := Delta{Day: point.Day}
		if raw := point.Used - point.DayStartUsed; raw >= 0 {
			entry.Delta, entry.HasDelta = raw, true
		}
		if totalUnits > 0 {
			entry.IdealShare, entry.HasShare = schedule.DayUnit(point.Day, loc)*(100/totalUnits), true
		}
		result = append(result, entry)
	}
	return result
}

func startOfDay(at time.Time, loc *time.Location) time.Time {
	y, m, d := at.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}
