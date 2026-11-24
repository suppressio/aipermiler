package advice

import (
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
)

// maxRatio caps one day's ratio so a day without use does not dominate the
// average (D-3.22, D6).
const maxRatio = 3

// Rating is the consistency over the last completed days.
type Rating struct {
	// Stars is 1 to 5.
	Stars int
	// Average is the mean of the capped daily ratios, at full precision.
	Average float64
}

// Rate returns the consistency rating over the completed local days of the last
// `days` days before today (D-3.22): per day min(3, share / delta), 3 when
// nothing was used; days off (share 0) and reset days (no delta) are skipped;
// today is never rated. It is false when no day is usable, when nothing was
// consumed on the rated days (five stars for an unused quota said nothing) or
// without pacing (totalUnits <= 0). Rolling-hours windows have no rating: the
// caller does not ask.
func Rate(points []history.DailyPoint, schedule quotaperiod.Schedule, totalUnits float64, now time.Time, loc *time.Location, days int) (Rating, bool) {
	today := startOfDay(now, loc)
	first := today.AddDate(0, 0, -days)
	var sum float64
	var count int
	consumed := false
	for _, day := range Deltas(points, schedule, totalUnits, loc) {
		if day.Day.Before(first) || !day.Day.Before(today) || !day.HasShare || day.IdealShare <= 0 || !day.HasDelta {
			continue
		}
		ratio := float64(maxRatio)
		if day.Delta > 0 {
			consumed = true
			ratio = min(maxRatio, day.IdealShare/day.Delta)
		}
		sum += ratio
		count++
	}
	if count == 0 || !consumed {
		return Rating{}, false
	}
	average := sum / float64(count)
	return Rating{Stars: stars(average), Average: average}, true
}

func stars(average float64) int {
	switch {
	case average >= 1.5:
		return 5
	case average >= 1.1:
		return 4
	case average >= 0.9:
		return 3
	case average >= 0.6:
		return 2
	default:
		return 1
	}
}
