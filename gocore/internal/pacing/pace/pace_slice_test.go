package pace_test

import (
	"math"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/pace"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
)

const epsilon = 1e-9

func near(got, want float64) bool { return math.Abs(got-want) <= epsilon }

func TestIdeal(t *testing.T) {
	t.Parallel()

	var cases []struct {
		ID, Description, Kind     string
		Total, Share, HoursPerDay float64
		PeriodLength              int
		Want                      float64
		OK                        bool
	}
	readFixture(t, "ideal.json", &cases)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			var got float64
			var ok bool
			switch tc.Kind {
			case "share":
				got, ok = pace.EvenShare(tc.Total)
			case "hourly":
				got, ok = pace.IdealHourly(tc.Share, tc.HoursPerDay)
			case "rolling":
				got, ok = pace.RollingIdealHourly(tc.PeriodLength)
			default:
				t.Fatalf("unknown kind %q", tc.Kind)
			}
			if ok != tc.OK || !near(got, tc.Want) {
				t.Fatalf("got %v, %v; want %v, %v", got, ok, tc.Want, tc.OK)
			}
		})
	}
}

func TestEfficiency(t *testing.T) {
	t.Parallel()

	var cases []struct {
		ID, Description      string
		Used, Total, Elapsed float64
		Want                 float64
		OK                   bool
	}
	readFixture(t, "efficiency.json", &cases)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got, ok := pace.Efficiency(tc.Used, tc.Total, tc.Elapsed)
			if ok != tc.OK || !near(got, tc.Want) {
				t.Fatalf("Efficiency = %v, %v; want %v, %v", got, ok, tc.Want, tc.OK)
			}
		})
	}
}

func TestRecentPace(t *testing.T) {
	t.Parallel()

	schedules := map[string]quotaperiod.Schedule{
		"weekdays":   {Enabled: true, Days: [7]float64{0, 1, 1, 1, 1, 1, 0}, HoursPerDay: 8},
		"halfFriday": {Enabled: true, Days: [7]float64{0, 1, 1, 1, 1, 0.5, 0}, HoursPerDay: 8},
		"disabled":   {Enabled: false, Days: [7]float64{0, 1, 1, 1, 1, 1, 0}, HoursPerDay: 8},
	}
	var cases []struct {
		ID, Description, Schedule, Now string
		// Rolling-days window start (RFC 3339); empty: a billing cycle.
		RollingStart string
		Points       [][3]any
		Want         float64
		OK           bool
	}
	readFixture(t, "recent.json", &cases)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			now, err := time.Parse(time.RFC3339, tc.Now)
			if err != nil {
				t.Fatal(err)
			}
			points := make([]history.DailyPoint, 0, len(tc.Points))
			for _, p := range tc.Points {
				day, err := time.Parse(time.DateOnly, p[0].(string)) //nolint:forcetypeassert // fixture shape
				if err != nil {
					t.Fatal(err)
				}
				points = append(points, history.DailyPoint{Day: day, Used: p[1].(float64), DayStartUsed: p[2].(float64)}) //nolint:forcetypeassert // fixture shape
			}
			period := quotaperiod.Period{Kind: quotaperiod.BillingCycle}
			if tc.RollingStart != "" {
				start, err := time.Parse(time.RFC3339, tc.RollingStart)
				if err != nil {
					t.Fatal(err)
				}
				period = quotaperiod.Period{Kind: quotaperiod.RollingDays, Start: start, End: start.AddDate(0, 0, 7)}
			}
			got, ok := pace.RecentPace(points, schedules[tc.Schedule], period, now, time.UTC)
			if ok != tc.OK || !near(got, tc.Want) {
				t.Fatalf("RecentPace = %v, %v; want %v, %v", got, ok, tc.Want, tc.OK)
			}
		})
	}
}

func TestProjectionAndAutonomy(t *testing.T) {
	t.Parallel()

	var fixture struct {
		Blend []struct {
			ID, Description       string
			Used, Elapsed, Recent float64
			HasRecent             bool
			Want                  float64
			OK                    bool
		}
		Projection []struct {
			ID, Description       string
			Used, Pace, Remaining float64
			HasPace               bool
			Want                  float64
		}
		Autonomy []struct {
			ID, Description string
			Used, Pace      float64
			HasPace, Inf    bool
			Want            float64
		}
	}
	readFixture(t, "projection.json", &fixture)
	for _, tc := range fixture.Blend {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got, ok := pace.Blend(tc.Used, tc.Elapsed, tc.Recent, tc.HasRecent)
			if ok != tc.OK || !near(got, tc.Want) {
				t.Fatalf("Blend = %v, %v; want %v, %v", got, ok, tc.Want, tc.OK)
			}
		})
	}
	for _, tc := range fixture.Projection {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			if got := pace.Projection(tc.Used, tc.Pace, tc.HasPace, tc.Remaining); !near(got, tc.Want) {
				t.Fatalf("Projection = %v; want %v", got, tc.Want)
			}
		})
	}
	for _, tc := range fixture.Autonomy {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got := pace.Autonomy(tc.Used, tc.Pace, tc.HasPace)
			if tc.Inf {
				if !math.IsInf(got, 1) {
					t.Fatalf("Autonomy = %v; want +Inf", got)
				}
				return
			}
			if !near(got, tc.Want) {
				t.Fatalf("Autonomy = %v; want %v", got, tc.Want)
			}
		})
	}
}
