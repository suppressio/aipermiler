package outlook

import "github.com/suppressio/aipermiler/gocore/internal/pacing/pace"

// Redistribution is the remaining quota spread over the working units from
// today on, next to the even share (§3.8).
type Redistribution struct {
	// PerUnit is q = rem / U in pp per working unit (D-3.18).
	PerUnit float64
	// EvenShare is s = 100 / N in pp per working unit.
	EvenShare float64
	// Units is U, the working units from today on, today whole.
	Units float64
}

// Ratio is rho = q / s (D-3.19), derived so it cannot disagree with q and s.
func (r Redistribution) Ratio() float64 {
	if r.EvenShare <= 0 {
		return 0
	}
	return r.PerUnit / r.EvenShare
}

// Redistribute returns what is left now over the working units from today on
// (D-3.18, D-3.19): q = rem / U, with the even share s = 100 / N and the ratio
// q / s. It follows the day's consumption and needs no history. It is false
// when U <= 0 or N <= 0.
func Redistribute(remaining, unitsFromToday, totalUnits float64) (Redistribution, bool) {
	share, ok := pace.EvenShare(totalUnits)
	if unitsFromToday <= 0 || !ok {
		return Redistribution{}, false
	}
	return Redistribution{PerUnit: max(0, remaining) / unitsFromToday, EvenShare: share, Units: unitsFromToday}, true
}
