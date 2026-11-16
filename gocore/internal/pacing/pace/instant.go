package pace

import (
	"math"
	"slices"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
)

// instantLookback is how far back the instant pace looks (D-3.11).
const instantLookback = time.Hour

// minInstantSpan is the shortest time between two samples that gives a pace;
// closer ones are provider rounding on a tiny interval (D-3.11).
const minInstantSpan = 5 * time.Minute

// InstantPace returns the pace v in pp/h over the last hour (D-3.11), and false
// when it cannot be measured.
//
// The newest sample at or before now is compared with the anchor: the last
// sample at or before now-1h but not older than now-2h; without one, the oldest
// sample inside the last hour. It is false with fewer than 2 samples, when the
// anchor is the newest sample itself, or when less than 5 minutes separate them.
// A drop (reset) counts as 0, never as a negative pace. Samples after now are
// ignored; the input is not modified.
func InstantPace(samples []history.Reading, now time.Time) (float64, bool) {
	points := make([]history.Reading, 0, len(samples))
	for _, sample := range samples {
		if !sample.At.After(now) {
			points = append(points, sample)
		}
	}
	if len(points) < 2 {
		return 0, false
	}
	slices.SortStableFunc(points, func(a, b history.Reading) int { return a.At.Compare(b.At) })

	latest := len(points) - 1
	cutoff := now.Add(-instantLookback)
	anchor := -1
	for i := latest; i >= 0; i-- {
		if points[i].At.After(cutoff) {
			anchor = i // the oldest inside the last hour, unless an older anchor is found
			continue
		}
		if !points[i].At.Before(cutoff.Add(-instantLookback)) {
			anchor = i
		}
		break
	}
	if anchor < 0 || anchor == latest {
		return 0, false
	}
	elapsed := points[latest].At.Sub(points[anchor].At)
	if elapsed < minInstantSpan {
		return 0, false
	}
	return math.Max(0, points[latest].Used-points[anchor].Used) / elapsed.Hours(), true
}
