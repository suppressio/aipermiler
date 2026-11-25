package pace

import (
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
)

// Trend is the direction of the instant consumption (D-3.28).
type Trend string

// The instant trends.
const (
	Accelerating Trend = "accelerating"
	SlowingDown  Trend = "slowing-down"
	Steady       Trend = "steady"
)

// trendLookback is how far back the earlier instant pace is taken (D-3.28).
const trendLookback = 30 * time.Minute

// InstantTrend compares the instant pace at now with the one 30 minutes
// earlier, each from the samples as they were at its moment (D-3.28). The
// difference counts only beyond the noise threshold 2 × (1/h₁ + 1/h₂) pp/h,
// where h₁ and h₂ are the hours each pace spans: integer values make each pace
// uncertain by up to 1 point over its span.
//
// It is false when either pace cannot be measured or a reset (used going down)
// falls inside either span. Whether the current read is fresh is the caller's
// to check. The input is not modified.
func InstantTrend(samples []history.Reading, now time.Time) (Trend, bool) {
	current, ok := instantSpan(samples, now)
	if !ok || current.dropped() {
		return "", false
	}
	earlier, ok := instantSpan(samples, now.Add(-trendLookback))
	if !ok || earlier.dropped() {
		return "", false
	}
	delta := current.pace() - earlier.pace()
	threshold := 2 * (1/current.hours() + 1/earlier.hours())
	switch {
	case delta > threshold:
		return Accelerating, true
	case delta < -threshold:
		return SlowingDown, true
	default:
		return Steady, true
	}
}
