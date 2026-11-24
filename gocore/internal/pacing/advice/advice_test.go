package advice_test

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/advice"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
)

// Scenario S of docs/PACING.md §0: Monday to Friday, billing cycle
// [2026-10-01, 2026-11-01), now Tuesday 2026-10-20 10:00, N = 22.
var (
	scenarioNow   = time.Date(2026, time.October, 20, 10, 0, 0, 0, time.UTC)
	scenarioStart = time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	scenarioEnd   = time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	scenario      = quotaperiod.Schedule{Enabled: true, Days: [7]float64{0, 1, 1, 1, 1, 1, 0}, HoursPerDay: 8}
)

const scenarioUnits = 22

func readFixture(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatal(err)
	}
}

func near(got, want float64) bool {
	return math.Abs(got-want) <= 1e-9
}

// points reads [day, used, dayStartUsed] triples.
func points(t *testing.T, raw [][]any) []history.DailyPoint {
	t.Helper()
	result := make([]history.DailyPoint, 0, len(raw))
	for _, entry := range raw {
		day, err := time.Parse(time.DateOnly, entry[0].(string))
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, history.DailyPoint{Day: day, Used: entry[1].(float64), DayStartUsed: entry[2].(float64)})
	}
	return result
}

func TestRateFixtures(t *testing.T) {
	t.Parallel()

	var fixtures []struct {
		ID, Description string
		Total           float64
		Points          [][]any
		OK              bool
		Stars           int
		Average         float64
	}
	readFixture(t, "rating.json", &fixtures)

	for _, tc := range fixtures {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got, ok := advice.Rate(points(t, tc.Points), scenario, tc.Total, scenarioNow, time.UTC, 7)
			if ok != tc.OK {
				t.Fatalf("ok = %v, want %v", ok, tc.OK)
			}
			if ok && (got.Stars != tc.Stars || !near(got.Average, tc.Average)) {
				t.Errorf("got %+v, want %d stars, average %v", got, tc.Stars, tc.Average)
			}
		})
	}
}

type chartCase struct {
	ID, Description string
	PastDays        int
	Upcoming        int
	NoPeriod        bool
	Points          [][]any
	PerUnit         float64
	HasPerUnit      bool
	TodayBudget     float64
	HasTodayBudget  bool
}

func (tc chartCase) input(t *testing.T) advice.ChartInput {
	t.Helper()
	in := advice.ChartInput{
		Points: points(t, tc.Points), Schedule: scenario, Start: scenarioStart, End: scenarioEnd,
		TotalUnits: scenarioUnits, Now: scenarioNow, Location: time.UTC, PastDays: tc.PastDays, UpcomingDays: tc.Upcoming,
		TodayBudget: tc.TodayBudget, HasTodayBudget: tc.HasTodayBudget, PerUnit: tc.PerUnit, HasPerUnit: tc.HasPerUnit,
	}
	if tc.NoPeriod {
		in.Start, in.TotalUnits = time.Time{}, 0
	}
	return in
}

func TestChartFixtures(t *testing.T) {
	t.Parallel()

	var fixtures []struct {
		chartCase
		// Optional fields are checked only when the fixture states them.
		Checks []struct {
			Day       string
			HasDelta  *bool
			Delta     *float64
			HasBudget *bool
			Budget    *float64
			HasShare  *bool
			Share     *float64
			Over      bool
		}
	}
	readFixture(t, "chart.json", &fixtures)

	for _, tc := range fixtures {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			slots := advice.Chart(tc.input(t))
			if len(slots) != tc.PastDays+tc.Upcoming {
				t.Fatalf("%d slots, want %d", len(slots), tc.PastDays+tc.Upcoming)
			}
			if last := slots[tc.PastDays-1]; !last.Day.Equal(scenarioNow.Truncate(24*time.Hour)) || last.Upcoming {
				t.Errorf("slot %d is %v, want today", tc.PastDays-1, last.Day)
			}
			for _, check := range tc.Checks {
				var slot *advice.Slot
				for i := range slots {
					if slots[i].Day.Format(time.DateOnly) == check.Day {
						slot = &slots[i]
					}
				}
				if slot == nil {
					t.Fatalf("no slot for %s", check.Day)
				}
				expect(t, check.Day+" delta", slot.HasDelta, slot.Delta, check.HasDelta, check.Delta)
				expect(t, check.Day+" budget", slot.HasBudget, slot.Budget, check.HasBudget, check.Budget)
				expect(t, check.Day+" share", slot.HasFullShare, slot.FullShare, check.HasShare, check.Share)
				if slot.OverBudget != check.Over {
					t.Errorf("%s over = %v, want %v", check.Day, slot.OverBudget, check.Over)
				}
			}
		})
	}
}

func TestSummarizeFixtures(t *testing.T) {
	t.Parallel()

	var fixtures []struct {
		chartCase
		Days           int
		Peak, Average  float64
		HasPeakAverage bool
		HasStreak      bool
		Streak         int
	}
	readFixture(t, "stats.json", &fixtures)

	for _, tc := range fixtures {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got := advice.Summarize(advice.Chart(tc.input(t)), scenarioNow, time.UTC)
			if got.Days != tc.Days || !near(got.Peak, tc.Peak) || !near(got.Average, tc.Average) ||
				got.HasPeakAverage != tc.HasPeakAverage || got.HasStreak != tc.HasStreak || got.Streak != tc.Streak {
				t.Errorf("got %+v, want %+v", got, tc)
			}
		})
	}
}

func TestTipFixtures(t *testing.T) {
	t.Parallel()

	var fixtures []struct {
		ID, Description string
		Used            float64
		Autonomy        float64
		HasAutonomy     bool
		Units           float64
		Preliminary     bool
		Days            float64
		HasDays         bool
		Rating          *advice.Rating
		Cause           *advice.Cause
		Keys            []advice.TipKey
		ReductionShown  float64
	}
	readFixture(t, "tip.json", &fixtures)

	for _, tc := range fixtures {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got := advice.TipCandidates(advice.TipInput{
				Used: tc.Used, Preliminary: tc.Preliminary, Autonomy: tc.Autonomy, HasAutonomy: tc.HasAutonomy,
				WorkingUnitsToReset: tc.Units, Rating: tc.Rating, DaysToReset: tc.Days, HasDaysToReset: tc.HasDays, Cause: tc.Cause,
			})
			if len(got) != len(tc.Keys) {
				t.Fatalf("got %v, want keys %v", got, tc.Keys)
			}
			for i, key := range tc.Keys {
				if got[i].Key != key {
					t.Errorf("candidate %d = %s, want %s", i, got[i].Key, key)
				}
			}
			if tc.ReductionShown > 0 && math.Round(got[0].Params["reduction"]) != tc.ReductionShown {
				t.Errorf("reduction = %v, want it to show as %v", got[0].Params["reduction"], tc.ReductionShown)
			}
		})
	}
}

func TestPickTip(t *testing.T) {
	t.Parallel()

	if _, ok := advice.PickTip(nil, 0.5); ok {
		t.Error("no candidates must give no tip")
	}
	candidates := []advice.Tip{{Key: advice.TipAutonomy}, {Key: advice.TipRating}}
	for draw, want := range map[float64]advice.TipKey{0: advice.TipAutonomy, 0.49: advice.TipAutonomy, 0.5: advice.TipRating, 1: advice.TipRating, -1: advice.TipAutonomy} {
		if got, _ := advice.PickTip(candidates, draw); got.Key != want {
			t.Errorf("draw %v = %s, want %s", draw, got.Key, want)
		}
	}
}

func expect(t *testing.T, what string, has bool, got float64, wantHas *bool, want *float64) {
	t.Helper()
	if wantHas != nil && has != *wantHas {
		t.Errorf("%s present = %v, want %v", what, has, *wantHas)
	}
	if want != nil && (!has || !near(got, *want)) {
		t.Errorf("%s = %v (%v), want %v", what, got, has, *want)
	}
}

func TestDeltas(t *testing.T) {
	t.Parallel()

	// D-3.21: the delta of a day is its last value minus its baseline, null
	// after a reset; the ideal share is s times the day's unit, 0 on a day off.
	got := advice.Deltas(points(t, [][]any{{"2026-10-17", 63.0, 60.0}, {"2026-10-15", 55.0, 52.0}, {"2026-10-16", 3.0, 55.0}}), scenario, scenarioUnits, time.UTC)
	want := []struct {
		day      string
		delta    float64
		hasDelta bool
		share    float64
	}{{"2026-10-15", 3, true, 100.0 / 22}, {"2026-10-16", 0, false, 100.0 / 22}, {"2026-10-17", 3, true, 0}}
	if len(got) != len(want) {
		t.Fatalf("got %d deltas, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Day.Format(time.DateOnly) != w.day || got[i].HasDelta != w.hasDelta || (w.hasDelta && !near(got[i].Delta, w.delta)) ||
			!got[i].HasShare || !near(got[i].IdealShare, w.share) {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], w)
		}
	}
	if none := advice.Deltas(points(t, [][]any{{"2026-10-15", 55.0, 52.0}}), scenario, 0, time.UTC); none[0].HasShare {
		t.Error("without pacing there is no ideal share")
	}
}
