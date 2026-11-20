package outlook

import "github.com/suppressio/aipermiler/gocore/internal/pacing/pace"

// preliminaryBelow is the elapsed working units under which projection and
// autonomy are flagged preliminary (§3.13).
const preliminaryBelow = 2

// Preliminary reports whether the elapsed working units E are too few for the
// projection to be trusted (§3.13): below 2 the warning colour and the pace
// override of the verdict are off.
func Preliminary(elapsedUnits float64) bool {
	return elapsedUnits < preliminaryBelow
}

// Summary is the projection and autonomy of a day-based window (§3.13).
type Summary struct {
	// Pace is the blended pace p; only meaningful when HasPace.
	Pace    float64
	HasPace bool
	// Projection is x + p * R in pp, not capped at 100.
	Projection float64
	// Autonomy is in working units; +Inf when unknown or not consuming.
	Autonomy float64
	// WorkingUnitsToReset is R.
	WorkingUnitsToReset float64
	Preliminary         bool
}

// Summarize builds the projection summary from the used quota x, the elapsed
// and remaining working units and the recent pace (D-3.8 to D-3.10, §3.13).
func Summarize(used, elapsedUnits, remainingUnits, recentPace float64, hasRecent bool) Summary {
	p, ok := pace.Blend(used, elapsedUnits, recentPace, hasRecent)
	return Summary{
		Pace:                p,
		HasPace:             ok,
		Projection:          pace.Projection(used, p, ok, remainingUnits),
		Autonomy:            pace.Autonomy(used, p, ok),
		WorkingUnitsToReset: remainingUnits,
		Preliminary:         Preliminary(elapsedUnits),
	}
}
