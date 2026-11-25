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
	span, ok := instantSpan(samples, now)
	if !ok {
		return 0, false
	}
	return span.pace(), true
}

// instantWindow is what one instant pace spans: the samples up to now, sorted,
// and the indexes of its anchor and of the newest sample.
type instantWindow struct {
	points         []history.Reading
	anchor, latest int
}

// hours is the time the pace spans, from the anchor to the newest sample.
func (w instantWindow) hours() float64 {
	return w.points[w.latest].At.Sub(w.points[w.anchor].At).Hours()
}

// pace is v (D-3.11): a drop counts as 0.
func (w instantWindow) pace() float64 {
	return math.Max(0, w.points[w.latest].Used-w.points[w.anchor].Used) / w.hours()
}

// dropped reports whether used goes down between two samples of the span.
func (w instantWindow) dropped() bool {
	for i := w.anchor + 1; i <= w.latest; i++ {
		if w.points[i].Used < w.points[i-1].Used {
			return true
		}
	}
	return false
}

// instantSpan finds the span of the instant pace at now (D-3.11), false when
// the pace cannot be measured.
func instantSpan(samples []history.Reading, now time.Time) (instantWindow, bool) {
	points := make([]history.Reading, 0, len(samples))
	for _, sample := range samples {
		if !sample.At.After(now) {
			points = append(points, sample)
		}
	}
	if len(points) < 2 {
		return instantWindow{}, false
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
		return instantWindow{}, false
	}
	if points[latest].At.Sub(points[anchor].At) < minInstantSpan {
		return instantWindow{}, false
	}
	return instantWindow{points: points, anchor: anchor, latest: latest}, true
}
