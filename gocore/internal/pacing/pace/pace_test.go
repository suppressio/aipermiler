package pace_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/pace"
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

func TestUsed(t *testing.T) {
	t.Parallel()

	var cases []struct {
		ID, Description, Unit string
		Used, Total, Want     float64
		OK                    bool
	}
	readFixture(t, "used.json", &cases)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			got, ok := pace.Used(tc.Used, tc.Total, pace.Unit(tc.Unit))
			if ok != tc.OK || got != tc.Want {
				t.Fatalf("Used = %v, %v; want %v, %v", got, ok, tc.Want, tc.OK)
			}
		})
	}
}

func TestRemaining(t *testing.T) {
	t.Parallel()

	var cases []struct {
		ID, Description string
		Used, Want      float64
	}
	readFixture(t, "remaining.json", &cases)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			if got := pace.Remaining(tc.Used); got != tc.Want {
				t.Fatalf("Remaining(%v) = %v; want %v", tc.Used, got, tc.Want)
			}
		})
	}
}

func TestTodayActivity(t *testing.T) {
	t.Parallel()

	var cases []struct {
		ID, Description string
		Baseline        *float64
		Session         string
		Samples         [][2]json.RawMessage
		OK              bool
		Start, End      time.Time
		Hours           float64
	}
	readFixture(t, "activity.json", &cases)
	now := time.Date(2026, time.October, 20, 15, 0, 0, 0, time.UTC)
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			samples := make([]history.Reading, 0, len(tc.Samples))
			for _, raw := range tc.Samples {
				var reading history.Reading
				if err := json.Unmarshal(raw[0], &reading.At); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw[1], &reading.Used); err != nil {
					t.Fatal(err)
				}
				samples = append(samples, reading)
			}
			var session time.Time
			if tc.Session != "" {
				var err error
				if session, err = time.Parse(time.RFC3339, tc.Session); err != nil {
					t.Fatal(err)
				}
			}
			span, ok := pace.TodayActivity(samples, tc.Baseline, session, now, time.UTC)
			if ok != tc.OK {
				t.Fatalf("ok = %v; want %v (span %+v)", ok, tc.OK, span)
			}
			if !ok {
				return
			}
			if !span.Start.Equal(tc.Start) || !span.End.Equal(tc.End) || span.Hours() != tc.Hours {
				t.Fatalf("span = %v..%v (%v h); want %v..%v (%v h)",
					span.Start, span.End, span.Hours(), tc.Start, tc.End, tc.Hours)
			}
		})
	}
}

func TestTodayActivityLocalDay(t *testing.T) {
	t.Parallel()

	// 23:30 UTC on the 19th is already the 20th in Rome (UTC+2 in October).
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Fatal(err)
	}
	baseline := 10.0
	samples := []history.Reading{
		{At: time.Date(2026, time.October, 19, 23, 30, 0, 0, time.UTC), Used: 10},
		{At: time.Date(2026, time.October, 19, 23, 50, 0, 0, time.UTC), Used: 11},
	}
	now := time.Date(2026, time.October, 20, 0, 10, 0, 0, time.UTC)
	span, ok := pace.TodayActivity(samples, &baseline, time.Time{}, now, rome)
	if !ok || span.Hours() < 0.33 || span.Hours() > 0.34 {
		t.Fatalf("span = %+v, %v; want 20 minutes in the local day", span, ok)
	}
}
