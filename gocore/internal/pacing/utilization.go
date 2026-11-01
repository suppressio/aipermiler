package pacing

// NormalizedUtilization returns the 0-100 percentage of total that used
// represents, and false when it cannot be computed (no positive total).
func NormalizedUtilization(used, total float64) (float64, bool) {
	if total <= 0 {
		return 0, false
	}
	pct := used / total * 100
	return min(max(pct, 0), 100), true
}
