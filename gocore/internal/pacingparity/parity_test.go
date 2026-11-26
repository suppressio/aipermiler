package pacingparity_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/pacingparity"
)

const scenarios = "../../../scripts/pacing-parity/scenarios"

func run(t *testing.T, file string) pacingparity.Output {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(scenarios, file))
	if err != nil {
		t.Fatal(err)
	}
	s, err := pacingparity.Parse(data)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	out, err := pacingparity.Run(s)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return out
}

func TestEveryShippedScenarioRuns(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join(scenarios, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("scenarios: %v, %d files", err, len(files))
	}
	for _, f := range files {
		out := run(t, filepath.Base(f))
		if len(out.Windows) == 0 {
			t.Errorf("%s: no window in the output", f)
		}
	}
}

// Mid-month scenario: 38.5 % used with 8 working units left over 22 (Wed 10-21
// 11:00, no activity span before the samples), so the remaining 61.5 % is
// 7.6875 per working unit against the even share 100/22.
func TestMidMonthReadingsAreExact(t *testing.T) {
	t.Parallel()
	w := run(t, "billing-cycle-midmonth.json").Windows[0]
	if w.Redistribution == nil || w.Redistribution.PerUnit != 7.6875 {
		t.Errorf("redistribution = %+v; want per unit 7.6875", w.Redistribution)
	}
	if w.Verdict == "" || w.Verdict == "no-pacing" {
		t.Errorf("verdict = %q; want a paced verdict", w.Verdict)
	}
}

func TestParseNeedsNameNowAndTimeZone(t *testing.T) {
	t.Parallel()
	if _, err := pacingparity.Parse([]byte(`{"name":"x"}`)); err == nil {
		t.Error("a scenario without now and timeZone parsed")
	}
}

func TestUnknownTimeZoneIsAnError(t *testing.T) {
	t.Parallel()
	s, err := pacingparity.Parse([]byte(`{"name":"x","now":"2026-10-21T11:00:00Z","timeZone":"Nowhere/Land"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pacingparity.Run(s); err == nil {
		t.Error("an unknown time zone ran")
	}
}

// Both sides must read a scenario the same way (#325 review): TypeScript
// knows only full, half and off days and defaults the renewal day to 1, and
// a read after now means nothing to either.
func TestParseRejectsWhatTheTwoSidesReadDifferently(t *testing.T) {
	t.Parallel()
	base := `"name":"x","now":"2026-10-21T11:00:00Z","timeZone":"UTC"`
	for name, body := range map[string]string{
		"weight 0.75":      `{` + base + `,"schedule":{"enabled":true,"days":{"sat":0.75},"hoursPerDay":8}}`,
		"unknown day":      `{` + base + `,"schedule":{"enabled":true,"days":{"saturday":1},"hoursPerDay":8}}`,
		"renewal day 32":   `{` + base + `,"renewalDay":32}`,
		"read after now":   `{` + base + `,"fresh":true,"readAt":"2026-10-21T11:00:01Z"}`,
		"negative renewal": `{` + base + `,"renewalDay":-1}`,
	} {
		if _, err := pacingparity.Parse([]byte(body)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestParseDefaultsTheRenewalDayToOneAsTypeScript(t *testing.T) {
	t.Parallel()
	s, err := pacingparity.Parse([]byte(`{"name":"x","now":"2026-10-21T11:00:00Z","timeZone":"UTC"}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.RenewalDay != 1 {
		t.Errorf("renewal day = %d; want 1", s.RenewalDay)
	}
}
