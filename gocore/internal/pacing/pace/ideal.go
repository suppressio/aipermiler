package pace

// EvenShare returns s = 100 / N in percentage points per working unit (D-3.4),
// and false when N <= 0: there is no pacing.
func EvenShare(totalUnits float64) (float64, bool) {
	if totalUnits <= 0 {
		return 0, false
	}
	return 100 / totalUnits, true
}

// IdealHourly returns s_h = s / hoursPerDay in pp/h for a day-based window
// (D-3.5), and false without a positive share or working day length.
func IdealHourly(share, hoursPerDay float64) (float64, bool) {
	if share <= 0 || hoursPerDay <= 0 {
		return 0, false
	}
	return share / hoursPerDay, true
}

// RollingIdealHourly returns 100 / periodLength in pp/h for a rolling-hours
// window (D-3.5: a 5 h window is 20 pp/h), and false when the length is unknown.
func RollingIdealHourly(periodLengthHours int) (float64, bool) {
	if periodLengthHours <= 0 {
		return 0, false
	}
	return 100 / float64(periodLengthHours), true
}

// negligibleUtilization is the used percentage below which a ratio on almost
// nothing is noise (D-3.6).
const negligibleUtilization = 0.1

// Efficiency returns idx = s / (x / E) (D-3.6): the ideal pace over the actual
// period-average pace, cumulative since the period start. It is false when
// x < 0.1, N <= 0 or E <= 0. It does not apply to rolling hours: the caller
// does not ask for it.
func Efficiency(used, totalUnits, elapsedUnits float64) (float64, bool) {
	share, ok := EvenShare(totalUnits)
	if !ok || elapsedUnits <= 0 || used < negligibleUtilization {
		return 0, false
	}
	return share / (used / elapsedUnits), true
}
