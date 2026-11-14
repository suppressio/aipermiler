package quotaperiod_test

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
)

func fixtures[T any](t *testing.T, path string) []T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []T
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("empty fixture file")
	}
	return rows
}

func instant(t *testing.T, value string) time.Time {
	t.Helper()
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func location(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func schedule(name string) quotaperiod.Schedule {
	s := quotaperiod.Schedule{Enabled: name != "disabled", Days: [7]float64{0, 1, 1, 1, 1, 1, 0}, HoursPerDay: 8}
	if name == "half-friday" {
		s.Days[time.Friday] = 0.5
	}
	return s
}

func closeTo(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > 1e-12 {
		t.Errorf("%s = %.17g, want %.17g", name, got, want)
	}
}

type unitsCase struct {
	ID, Description, Operation, Schedule string
	Kind                                 quotaperiod.Kind
	Start, End, Now                      string
	WorkedHours                          *float64
	ExpectedBetween                      float64
	Expected                             quotaperiod.WorkingUnits
}

func TestWorkingUnitsFixtures(t *testing.T) {
	t.Parallel()
	for _, tc := range fixtures[unitsCase](t, "testdata/units.json") {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()
			loc := location(t)
			s := schedule(tc.Schedule)
			if tc.Operation == "between" {
				got := quotaperiod.WorkingUnitsBetween(instant(t, tc.Start), instant(t, tc.End), loc, s)
				closeTo(t, "W", got, tc.ExpectedBetween)
				return
			}
			if tc.Start == "" {
				tc.Start = "2026-10-01T00:00:00+02:00"
			}
			if tc.End == "" {
				tc.End = "2026-11-01T00:00:00+01:00"
			}
			if tc.Now == "" {
				tc.Now = "2026-10-20T10:00:00+02:00"
			}
			if tc.Kind == "" {
				tc.Kind = quotaperiod.BillingCycle
			}
			p := quotaperiod.Period{Kind: tc.Kind, Start: instant(t, tc.Start), End: instant(t, tc.End)}
			now := instant(t, tc.Now)
			got, ok := p.Units(now, loc, s, tc.WorkedHours)
			if !ok {
				t.Fatal("day units unexpectedly unavailable")
			}
			closeTo(t, "N", got.Total, tc.Expected.Total)
			closeTo(t, "E", got.Elapsed, tc.Expected.Elapsed)
			closeTo(t, "R", got.Remaining, tc.Expected.Remaining)
			closeTo(t, "U", got.FromToday, tc.Expected.FromToday)
			closeTo(t, "e", got.TodayElapsed, tc.Expected.TodayElapsed)
			closeTo(t, "H", got.RemainingHours, tc.Expected.RemainingHours)
			closeTo(t, "I-8 E + R = N", got.Elapsed+got.Remaining, got.Total)
			if got.Elapsed < 0 || got.Remaining < 0 || got.TodayElapsed < 0 || got.TodayElapsed > s.DayUnit(now, loc)+1e-12 || got.FromToday+1e-12 < got.Remaining {
				t.Errorf("I-8 invalid units: %+v", got)
			}
		})
	}
}

type periodCase struct {
	ID, Description                         string
	Kind                                    quotaperiod.Kind
	Length, ManualDay                       int
	Reset, ProviderRenewal, Now, Start, End string
	DayUnits                                bool
	HoursLeft, DaysLeft                     *float64
}

func TestPeriodFixtures(t *testing.T) {
	t.Parallel()
	for _, tc := range fixtures[periodCase](t, "testdata/periods.json") {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()
			loc := location(t)
			if tc.Now == "" {
				tc.Now = "2026-10-20T10:00:00+02:00"
			}
			now := instant(t, tc.Now)
			p, err := quotaperiod.ResolvePeriod(
				quotaperiod.Window{Kind: tc.Kind, Length: tc.Length, ResetsAt: instant(t, tc.Reset)},
				instant(t, tc.ProviderRenewal), quotaperiod.RenewalRule{Type: "dayOfMonth", Day: tc.ManualDay}, now, loc)
			if err != nil {
				t.Fatal(err)
			}
			if p.Kind != tc.Kind || !p.Start.Equal(instant(t, tc.Start)) || !p.End.Equal(instant(t, tc.End)) {
				t.Fatalf("period = %+v; want %s [%s, %s)", p, tc.Kind, tc.Start, tc.End)
			}
			_, ok := p.Units(now, loc, schedule(""), nil)
			if ok != tc.DayUnits {
				t.Errorf("day units available = %v, want %v", ok, tc.DayUnits)
			}
			hours, hasHours := p.HoursUntilReset(now)
			if hasHours != !p.End.IsZero() {
				t.Errorf("hours availability = %v", hasHours)
			}
			if tc.HoursLeft != nil {
				closeTo(t, "hours left", hours, *tc.HoursLeft)
			}
			days, hasDays := p.DaysUntilReset(now, loc)
			if hasDays != !p.End.IsZero() {
				t.Errorf("days availability = %v", hasDays)
			}
			if tc.DaysLeft != nil {
				closeTo(t, "days left", days, *tc.DaysLeft)
			}
		})
	}
}

func TestRenewalFixtures(t *testing.T) {
	t.Parallel()
	type renewalCase struct {
		ID, Description, Type, Now, Expected string
		Day                                  int
		Error                                bool
	}
	for _, tc := range fixtures[renewalCase](t, "testdata/renewal.json") {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()
			if tc.Type == "" {
				tc.Type = "dayOfMonth"
			}
			got, err := quotaperiod.ResolveRenewalDate(quotaperiod.RenewalRule{Type: tc.Type, Day: tc.Day}, instant(t, tc.Now), location(t))
			if (err != nil) != tc.Error {
				t.Fatalf("error = %v, want error %v", err, tc.Error)
			}
			if !got.Equal(instant(t, tc.Expected)) {
				t.Errorf("renewal = %v, want %s", got, tc.Expected)
			}
		})
	}
}
