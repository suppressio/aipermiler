package pace_test

import (
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/pace"
)

func TestInstantPace(t *testing.T) {
	t.Parallel()

	var cases []struct {
		ID, Description, Now string
		Samples              [][2]any
		Want                 float64
		OK                   bool
	}
	readFixture(t, "instant.json", &cases)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			now, err := time.Parse(time.RFC3339, tc.Now)
			if err != nil {
				t.Fatal(err)
			}
			samples := make([]history.Reading, 0, len(tc.Samples))
			for _, s := range tc.Samples {
				at, err := time.Parse(time.RFC3339, s[0].(string)) //nolint:forcetypeassert // fixture shape
				if err != nil {
					t.Fatal(err)
				}
				samples = append(samples, history.Reading{At: at, Used: s[1].(float64)}) //nolint:forcetypeassert // fixture shape
			}
			before := append([]history.Reading(nil), samples...)
			got, ok := pace.InstantPace(samples, now)
			if ok != tc.OK || !near(got, tc.Want) {
				t.Fatalf("InstantPace = %v, %v; want %v, %v", got, ok, tc.Want, tc.OK)
			}
			for i := range samples {
				if samples[i] != before[i] {
					t.Fatal("InstantPace modified its input")
				}
			}
		})
	}
}

func TestCeilingTargetGauge(t *testing.T) {
	t.Parallel()

	var fixture struct {
		Ceiling []struct {
			ID, Description              string
			Remaining, Working, Calendar float64
			Want                         float64
			OK                           bool
		}
		Target []struct {
			ID, Description          string
			Ceiling, Ideal, Want     float64
			HasCeiling, HasIdeal, OK bool
		}
		Gauge []struct {
			ID, Description string
			V, Target, Want float64
		}
	}
	readFixture(t, "target.json", &fixture)
	for _, tc := range fixture.Ceiling {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got, ok := pace.Ceiling(tc.Remaining, tc.Working, tc.Calendar)
			if ok != tc.OK || !near(got, tc.Want) {
				t.Fatalf("Ceiling = %v, %v; want %v, %v", got, ok, tc.Want, tc.OK)
			}
		})
	}
	for _, tc := range fixture.Target {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got, ok := pace.Target(tc.Ceiling, tc.HasCeiling, tc.Ideal, tc.HasIdeal)
			if ok != tc.OK || !near(got, tc.Want) {
				t.Fatalf("Target = %v, %v; want %v, %v", got, ok, tc.Want, tc.OK)
			}
		})
	}
	for _, tc := range fixture.Gauge {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			if got := pace.GaugePosition(tc.V, tc.Target); !near(got, tc.Want) {
				t.Fatalf("GaugePosition(%v, %v) = %v; want %v", tc.V, tc.Target, got, tc.Want)
			}
		})
	}
}
