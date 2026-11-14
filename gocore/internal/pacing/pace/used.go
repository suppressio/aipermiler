// Package pace computes the base pacing quantities of one quota window
// (docs/PACING.md §3.1): used, remaining and today's activity. It is pure: no
// I/O and no hidden clock. Undefined values are reported with an explicit ok.
package pace

import "math"

// Unit is the unit of a quota window, as in the JSON contract.
type Unit string

// Quota window units.
const (
	Percentage Unit = "percentage"
	Count      Unit = "count"
)

// Used returns the normalized utilization x of a window in percentage points
// (D-3.1). It is not clamped above: 103 stays 103. A count needs a positive
// total, otherwise there is no utilization. Negative and non-finite input is
// not a utilization either.
func Used(used, total float64, unit Unit) (float64, bool) {
	var x float64
	switch unit {
	case Percentage:
		x = used
	case Count:
		if total <= 0 {
			return 0, false
		}
		x = used / total * 100
	default:
		return 0, false
	}
	if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 {
		return 0, false
	}
	return x, true
}

// Remaining returns rem = max(0, 100 - x) in percentage points (D-3.2).
func Remaining(used float64) float64 {
	return max(0, 100-used)
}
