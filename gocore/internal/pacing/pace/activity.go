package pace

import (
	"slices"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
)

// riseThreshold is the increase in percentage points above which a sample
// counts as activity (D-3.3); smaller ones are provider noise.
const riseThreshold = 0.05

// thresholdTolerance absorbs float64 rounding in the subtraction, so that a
// rise of exactly 0.05 pp (57.05 - 57.00) stays noise.
const thresholdTolerance = 1e-9

// Span is the period of a local day in which the quota was being used.
type Span struct {
	Start time.Time
	End   time.Time
}

// Hours returns the worked hours of the span, End - Start (D-3.3).
func (s Span) Hours() float64 {
	return s.End.Sub(s.Start).Hours()
}

// TodayActivity returns today's activity span from the samples of the local day
// of now in loc (D-3.3), and false when there is none.
//
// Each sample is compared with the previous one, the first with baseline (the
// day's baseline, history.DailyPoint.DayStartUsed); without a baseline the
// first sample has nothing to be compared with. A rise above 0.05 pp is
// activity, a drop is a reset and not activity: the next comparison then starts
// from the lower value. Start is the sample before the first rise, or the first
// rise itself when it is the day's first sample; End is the last rise.
//
// firstSession is the start of the day's first local Claude Code session (zero
// when unknown). It moves Start earlier, never later, and is ignored when it
// falls on another local day. Samples of other days, or after now, are ignored.
func TodayActivity(samples []history.Reading, baseline *float64, firstSession, now time.Time, loc *time.Location) (Span, bool) {
	dayStart := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)

	today := make([]history.Reading, 0, len(samples))
	for _, sample := range samples {
		if !sample.At.Before(dayStart) && sample.At.Before(dayEnd) && !sample.At.After(now) {
			today = append(today, sample)
		}
	}
	slices.SortStableFunc(today, func(a, b history.Reading) int { return a.At.Compare(b.At) })

	var span Span
	found := false
	previous := history.Reading{}
	hasPrevious := false
	if baseline != nil {
		previous, hasPrevious = history.Reading{Used: *baseline}, true
	}
	for _, sample := range today {
		if hasPrevious && sample.Used-previous.Used > riseThreshold+thresholdTolerance {
			if !found {
				span.Start = sample.At
				if !previous.At.IsZero() {
					span.Start = previous.At
				}
				found = true
			}
			span.End = sample.At
		}
		previous, hasPrevious = sample, true
	}
	if !found {
		return Span{}, false
	}
	if !firstSession.IsZero() && !firstSession.Before(dayStart) && firstSession.Before(span.Start) {
		span.Start = firstSession
	}
	return span, true
}
