// Package insights computes the local insights of docs/PACING.md §3.15: the
// yield of the quota (output tokens per percentage point) and the consumption
// cause (large context on the heavy days). It is pure: no I/O and no hidden
// clock. A quantity that does not meet its thresholds is absent (ok false),
// never zero.
package insights

import (
	"math"
	"slices"
	"strings"
)

// Thresholds of §3.15.
const (
	yieldMinDays       = 3
	yieldMinTotalDelta = 1.0 // percentage points: below this the ratio is noise
	causeMinDays       = 5
	causeMinRatio      = 1.5
	causeMinGapPoints  = 20.0
)

// Delta is the quota consumption of one local day (YYYY-MM-DD), in percentage
// points. Known is false when the day has no attributable delta (a reset in
// between, D-3.16).
type Delta struct {
	Date  string
	Delta float64
	Known bool
}

// Tokens is the local Claude Code output of one local day.
type Tokens struct {
	Date                    string
	OutputTokens            int64
	HighContextOutputTokens int64
}

// Yield is the result of D-3.26.
type Yield struct {
	TokensPerPercent int64
	// TrendPercent is the yield of the second half over the first, in %,
	// rounded to 0.1. TrendKnown is false when it cannot be computed.
	TrendPercent float64
	TrendKnown   bool
	DaysCompared int
}

// Cause is the result of D-3.27, shares in whole percent.
type Cause struct {
	HighDaysHighContextPercent int
	LowDaysHighContextPercent  int
	DaysCompared               int
}

type pairedDay struct {
	date                string
	delta               float64
	output, highContext float64
}

// pair keeps the days present in both sources with a positive delta and
// output tokens, oldest first. The first token entry of a date wins.
func pair(local []Tokens, deltas []Delta) []pairedDay {
	byDate := make(map[string]Tokens, len(local))
	for _, l := range local {
		if _, seen := byDate[l.Date]; !seen {
			byDate[l.Date] = l
		}
	}
	var days []pairedDay
	for _, d := range deltas {
		l, found := byDate[d.Date]
		if !d.Known || !(d.Delta > 0) || math.IsInf(d.Delta, 0) || !found || l.OutputTokens <= 0 {
			continue
		}
		days = append(days, pairedDay{d.Date, d.Delta, float64(l.OutputTokens), float64(l.HighContextOutputTokens)})
	}
	slices.SortStableFunc(days, func(a, b pairedDay) int { return strings.Compare(a.date, b.date) })
	return days
}

func yieldOf(days []pairedDay) (float64, bool) {
	var delta, output float64
	for _, d := range days {
		delta += d.delta
		output += d.output
	}
	if delta <= 0 {
		return 0, false
	}
	return output / delta, true
}

// TokenYield computes D-3.26. ok is false below 3 compared days or below 1 pp
// of total consumption.
func TokenYield(local []Tokens, deltas []Delta) (Yield, bool) {
	days := pair(local, deltas)
	if len(days) < yieldMinDays {
		return Yield{}, false
	}
	var total float64
	for _, d := range days {
		total += d.delta
	}
	if total < yieldMinTotalDelta {
		return Yield{}, false
	}
	overall, ok := yieldOf(days)
	if !ok {
		return Yield{}, false
	}
	half := len(days) / 2
	result := Yield{TokensPerPercent: int64(math.Round(overall)), DaysCompared: len(days)}
	first, firstOK := yieldOf(days[:half])
	second, secondOK := yieldOf(days[len(days)-half:])
	if firstOK && secondOK && first > 0 {
		result.TrendPercent = math.Round((second/first-1)*1000) / 10
		result.TrendKnown = true
	}
	return result, true
}

func median(values []float64) float64 {
	sorted := slices.Sorted(slices.Values(values))
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// highContextShare is the share in % of output tokens produced above 150k context.
func highContextShare(days []pairedDay) float64 {
	var output, high float64
	for _, d := range days {
		output += d.output
		high += d.highContext
	}
	if output <= 0 {
		return 0
	}
	return high / output * 100
}

// ConsumptionCause computes D-3.27. ok is false below 5 compared days, with no
// heavy or no light day, and unless the heavy days' high-context share is at
// least 1.5 times and 20 points above the others'.
func ConsumptionCause(local []Tokens, deltas []Delta) (Cause, bool) {
	days := pair(local, deltas)
	if len(days) < causeMinDays {
		return Cause{}, false
	}
	values := make([]float64, len(days))
	for i, d := range days {
		values[i] = d.delta
	}
	mid := median(values)
	var high, low []pairedDay
	for _, d := range days {
		if d.delta > mid {
			high = append(high, d)
		} else {
			low = append(low, d)
		}
	}
	if len(high) == 0 || len(low) == 0 {
		return Cause{}, false
	}
	highShare, lowShare := highContextShare(high), highContextShare(low)
	if highShare < lowShare*causeMinRatio || highShare-lowShare < causeMinGapPoints {
		return Cause{}, false
	}
	return Cause{
		HighDaysHighContextPercent: int(math.Round(highShare)),
		LowDaysHighContextPercent:  int(math.Round(lowShare)),
		DaysCompared:               len(days),
	}, true
}
