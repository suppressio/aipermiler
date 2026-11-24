package insights_test

import (
	"fmt"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/insights"
)

// days builds consecutive dates 2026-10-01.. with the given deltas and tokens.
func days(deltas []float64, output, high []int64) ([]insights.Delta, []insights.Tokens) {
	var ds []insights.Delta
	var ts []insights.Tokens
	for i, d := range deltas {
		date := fmt.Sprintf("2026-10-%02d", i+1)
		ds = append(ds, insights.Delta{Date: date, Delta: d, Known: true})
		ts = append(ts, insights.Tokens{Date: date, OutputTokens: output[i], HighContextOutputTokens: high[i]})
	}
	return ds, ts
}

func TestYield(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id, name string
		deltas   []float64
		output   []int64
		want     insights.Yield
		wantOK   bool
	}{
		{"Y-1", "tokens/delta 30k/3, 45k/3, 60k/3", []float64{3, 3, 3}, []int64{30000, 45000, 60000},
			insights.Yield{TokensPerPercent: 15000, TrendPercent: 100, TrendKnown: true, DaysCompared: 3}, true},
		{"Y-2", "2 days only", []float64{3, 3}, []int64{30000, 45000}, insights.Yield{}, false},
		{"Y-3", "total delta 0.8 pp", []float64{0.3, 0.3, 0.2}, []int64{3000, 3000, 3000}, insights.Yield{}, false},
		{"D-3.26", "boundary: exactly 1 pp over 3 days is enough", []float64{0.5, 0.25, 0.25}, []int64{500, 500, 500},
			insights.Yield{TokensPerPercent: 1500, TrendPercent: 100, TrendKnown: true, DaysCompared: 3}, true},
	}
	for _, tc := range cases {
		t.Run(tc.id+" "+tc.name, func(t *testing.T) {
			t.Parallel()

			zero := make([]int64, len(tc.output))
			ds, ts := days(tc.deltas, tc.output, zero)
			got, ok := insights.TokenYield(ts, ds)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestYieldDaysPairing(t *testing.T) {
	t.Parallel()

	// D-3.26: only days in both sources, with a positive delta and output tokens.
	ds := []insights.Delta{
		{Date: "2026-10-01", Delta: 2, Known: true},
		{Date: "2026-10-02", Delta: 2, Known: true},
		{Date: "2026-10-03", Delta: 2, Known: true},
		{Date: "2026-10-04", Known: false},
		{Date: "2026-10-05", Delta: 0, Known: true},
		{Date: "2026-10-06", Delta: 2, Known: true}, // no local day
		{Date: "2026-10-07", Delta: 2, Known: true}, // no output tokens
	}
	ts := []insights.Tokens{
		{Date: "2026-10-03", OutputTokens: 1000},
		{Date: "2026-10-01", OutputTokens: 1000},
		{Date: "2026-10-02", OutputTokens: 1000},
		{Date: "2026-10-04", OutputTokens: 1000},
		{Date: "2026-10-05", OutputTokens: 1000},
		{Date: "2026-10-07", OutputTokens: 0},
	}
	got, ok := insights.TokenYield(ts, ds)
	if !ok || got.DaysCompared != 3 || got.TokensPerPercent != 500 {
		t.Fatalf("got %+v, %v", got, ok)
	}
}

func TestCause(t *testing.T) {
	t.Parallel()

	const total = 100000
	// high-context tokens per day for a given share in percent
	share := func(p ...int64) []int64 {
		out := make([]int64, len(p))
		for i, v := range p {
			out[i] = total * v / 100
		}
		return out
	}
	cases := []struct {
		id, name string
		deltas   []float64
		shares   []int64
		want     insights.Cause
		wantOK   bool
	}{
		{"Y-4", "deltas 1..5, 60 % vs 20 %", []float64{1, 2, 3, 4, 5}, share(20, 20, 20, 60, 60),
			insights.Cause{HighDaysHighContextPercent: 60, LowDaysHighContextPercent: 20, DaysCompared: 5}, true},
		{"Y-5", "30 % vs 20 %: gap 10 < 20", []float64{1, 2, 3, 4, 5}, share(20, 20, 20, 30, 30), insights.Cause{}, false},
		{"Y-6", "4 days", []float64{1, 2, 3, 4}, share(20, 20, 60, 60), insights.Cause{}, false},
		{"D-3.27", "ratio below 1.5x with a gap over 20 points", []float64{1, 2, 3, 4, 5}, share(70, 70, 70, 90, 90), insights.Cause{}, false},
		{"D-3.27", "exactly 1.5x and 20 points is stated", []float64{1, 2, 3, 4, 5}, share(40, 40, 40, 60, 60),
			insights.Cause{HighDaysHighContextPercent: 60, LowDaysHighContextPercent: 40, DaysCompared: 5}, true},
		{"D-3.27", "all deltas equal: no heavy day", []float64{2, 2, 2, 2, 2}, share(0, 0, 90, 90, 90), insights.Cause{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.id+" "+tc.name, func(t *testing.T) {
			t.Parallel()

			output := make([]int64, len(tc.deltas))
			for i := range output {
				output[i] = total
			}
			ds, ts := days(tc.deltas, output, tc.shares)
			got, ok := insights.ConsumptionCause(ts, ds)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
