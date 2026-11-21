package outlook_test

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/outlook"
)

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

// near compares with a tolerance: the tables of docs/PACING.md show rounded
// values.
func near(got, want, tolerance float64) bool {
	return math.Abs(got-want) <= tolerance
}

func TestBudgetFixtures(t *testing.T) {
	t.Parallel()

	var fixtures struct {
		TodayBudget []struct {
			ID, Description string
			Baseline        float64
			HasBaseline     bool
			Units, DayUnit  float64
			OK              bool
			Want            float64
		}
		UsedToday []struct {
			ID, Description string
			Used, Baseline  float64
			Want            float64
		}
		PaceAlert []struct {
			ID, Description string
			UsedToday       float64
			Budget          float64
			Want            bool
		}
	}
	readFixture(t, "budget.json", &fixtures)

	for _, tc := range fixtures.TodayBudget {
		t.Run("budget "+tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got, ok := outlook.TodayBudget(tc.Baseline, tc.HasBaseline, tc.Units, tc.DayUnit)
			if ok != tc.OK || !near(got, tc.Want, 1e-9) {
				t.Fatalf("TodayBudget = %v, %v; want %v, %v", got, ok, tc.Want, tc.OK)
			}
		})
	}
	for _, tc := range fixtures.UsedToday {
		t.Run("used today "+tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			if got := outlook.UsedToday(tc.Used, tc.Baseline); !near(got, tc.Want, 1e-9) {
				t.Fatalf("UsedToday(%v, %v) = %v; want %v", tc.Used, tc.Baseline, got, tc.Want)
			}
		})
	}
	for _, tc := range fixtures.PaceAlert {
		t.Run("alert "+tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			if got := outlook.PaceAlert(tc.UsedToday, tc.Budget); got != tc.Want {
				t.Fatalf("PaceAlert(%v, %v) = %v; want %v", tc.UsedToday, tc.Budget, got, tc.Want)
			}
		})
	}
}

func TestRedistribute(t *testing.T) {
	t.Parallel()

	var cases []struct {
		ID, Description         string
		Remaining, Units, Total float64
		OK                      bool
		Q, S, Ratio             float64
	}
	readFixture(t, "redistribution.json", &cases)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got, ok := outlook.Redistribute(tc.Remaining, tc.Units, tc.Total)
			if ok != tc.OK {
				t.Fatalf("ok = %v; want %v", ok, tc.OK)
			}
			if !ok {
				return
			}
			if !near(got.PerUnit, tc.Q, 1e-9) || !near(got.EvenShare, tc.S, 1e-9) || !near(got.Ratio(), tc.Ratio, 1e-9) || got.Units != tc.Units {
				t.Fatalf("Redistribute = %+v; want q %v s %v ratio %v", got, tc.Q, tc.S, tc.Ratio)
			}
		})
	}
}

func TestHourlyOutlook(t *testing.T) {
	t.Parallel()

	var cases []struct {
		ID, Description string
		Used, Pace      float64
		HasPace         bool
		HoursLeft       float64
		HasReset        bool
		OK              bool
		Projected       float64
		HasAutonomy     bool
		Autonomy        float64
		Passed          bool
	}
	readFixture(t, "hourly.json", &cases)
	now := time.Date(2026, time.October, 20, 10, 0, 0, 0, time.UTC)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			resetsAt := now.Add(time.Duration(tc.HoursLeft * float64(time.Hour)))
			got, ok := outlook.HourlyOutlook(tc.Used, tc.Pace, tc.HasPace, resetsAt, tc.HasReset, now)
			if ok != tc.OK {
				t.Fatalf("ok = %v; want %v", ok, tc.OK)
			}
			if !ok {
				return
			}
			if got.ResetPassed != tc.Passed || got.HasAutonomy != tc.HasAutonomy ||
				!near(got.ProjectedAtReset, tc.Projected, 1e-9) || !near(got.AutonomyHours, tc.Autonomy, 0.01) {
				t.Fatalf("HourlyOutlook = %+v; want projected %v autonomy %v (%v) passed %v",
					got, tc.Projected, tc.Autonomy, tc.HasAutonomy, tc.Passed)
			}
			if tc.Passed && got.HoursLeft != 0 {
				t.Fatalf("HoursLeft = %v; want 0 with the reset passed", got.HoursLeft)
			}
		})
	}
}

func TestJudge(t *testing.T) {
	t.Parallel()

	var cases []struct {
		ID, Description string
		Used            float64
		Hourly          *struct {
			Projected   float64
			HasAutonomy bool
			Autonomy    float64
			Passed      bool
		}
		Q, S              float64
		HasRedistribution bool
		Pace              float64
		HasPace           bool
		Preliminary       bool
		Autonomy          float64
		Projection        float64
		Remaining         float64
		Want              outlook.Verdict
		Reason            outlook.Reason
		AutonomyHours     float64
	}
	readFixture(t, "verdict.json", &cases)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			in := outlook.VerdictInput{Used: tc.Used}
			if tc.HasPace {
				in.Summary = &outlook.Summary{
					Pace: tc.Pace, HasPace: true, Preliminary: tc.Preliminary, Projection: tc.Projection,
					Autonomy: tc.Autonomy, HasAutonomy: tc.Autonomy > 0, WorkingUnitsToReset: tc.Remaining,
				}
			}
			if h := tc.Hourly; h != nil {
				in.Hourly = &outlook.Hourly{ProjectedAtReset: h.Projected, HasAutonomy: h.HasAutonomy, AutonomyHours: h.Autonomy, ResetPassed: h.Passed}
			}
			if tc.HasRedistribution {
				in.Redistribution = &outlook.Redistribution{PerUnit: tc.Q, EvenShare: tc.S}
			}
			got := outlook.Judge(in)
			if got.Verdict != tc.Want || got.Reason != tc.Reason || got.AutonomyHours != tc.AutonomyHours || got.HasAutonomy != (tc.AutonomyHours > 0) {
				t.Fatalf("Judge = %+v; want %v (%q) autonomy hours %v", got, tc.Want, tc.Reason, tc.AutonomyHours)
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	var cases []struct {
		ID, Description    string
		Used, Elapsed      float64
		Remaining, Recent  float64
		HasRecent, HasPace bool
		Pace, Projection   float64
		Autonomy           float64
		AutonomyInf        bool
		Preliminary        bool
	}
	readFixture(t, "summary.json", &cases)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got := outlook.Summarize(tc.Used, tc.Elapsed, tc.Remaining, tc.Recent, tc.HasRecent)
			// Unknown autonomy is a flag, not +Inf: the value goes into JSON.
			autonomyOK := got.HasAutonomy && near(got.Autonomy, tc.Autonomy, 0.1) && !math.IsInf(got.Autonomy, 0)
			if tc.AutonomyInf {
				autonomyOK = !got.HasAutonomy && got.Autonomy == 0
			}
			if got.HasPace != tc.HasPace || got.Preliminary != tc.Preliminary || !autonomyOK ||
				!near(got.Pace, tc.Pace, 0.01) || !near(got.Projection, tc.Projection, 0.1) || got.WorkingUnitsToReset != tc.Remaining {
				t.Fatalf("Summarize = %+v; want %+v", got, tc)
			}
		})
	}
}
