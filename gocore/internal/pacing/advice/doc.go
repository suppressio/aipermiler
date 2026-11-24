// Package advice turns the daily history of a window into the readings that
// judge how its quota was spent: the daily deltas, the consistency rating
// (docs/PACING.md §3.11), the chart slots with peak, average and streak
// (§3.12) and the daily tips (§3.14). It is pure: no I/O and no hidden clock.
// Values keep full precision (I-4); rounding is for the snapshot.
package advice
