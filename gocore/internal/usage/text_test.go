package usage_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

func ptr[T any](value T) *T { return &value }

func TestRenderText(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 31, 21, 10, 0, 0, time.UTC)
	loc := time.FixedZone("Test", 2*60*60)
	tests := []struct {
		name   string
		window usage.QuotaWindow
		want   string
	}{
		{"hours", usage.QuotaWindow{PeriodType: usage.PeriodRollingHours, PeriodLength: ptr(5.0), Unit: usage.UnitPercentage, Used: 21, ResetsAt: ptr(now.Add(time.Hour))}, "5h 21% · resets 00:10"},
		{"days", usage.QuotaWindow{PeriodType: usage.PeriodRollingDays, PeriodLength: ptr(7.0), Unit: usage.UnitPercentage, Used: 41.6, ResetsAt: ptr(now.Add(7 * 24 * time.Hour))}, "7d 42% · resets 2026-11-07 23:10"},
		{"before 24 hours", usage.QuotaWindow{Label: "Quota", Unit: usage.UnitPercentage, Used: 10, ResetsAt: ptr(now.Add(24*time.Hour - time.Second))}, "Quota 10% · resets 23:09"},
		{"exactly 24 hours", usage.QuotaWindow{Label: "Quota", Unit: usage.UnitPercentage, Used: 10, ResetsAt: ptr(now.Add(24 * time.Hour))}, "Quota 10% · resets 2026-11-01 23:10"},
		{"past reset", usage.QuotaWindow{Label: "Quota", Unit: usage.UnitPercentage, ResetsAt: ptr(now.Add(-time.Hour))}, "Quota 0% · resets 2026-10-31 22:10"},
		{"count", usage.QuotaWindow{Label: "Monthly", PeriodType: usage.PeriodBillingCycle, PeriodLength: ptr(1.0), Unit: usage.UnitCount, Used: 250, Total: ptr(1000.0)}, "Monthly 250/1000 (25%)"},
		{"half percentage", usage.QuotaWindow{Label: "Quota", Unit: usage.UnitPercentage, Used: 20.5}, "Quota 21%"},
		{"half count percentage", usage.QuotaWindow{Label: "Quota", Unit: usage.UnitCount, Used: 41, Total: ptr(200.0)}, "Quota 41/200 (21%)"},
		{"count without total", usage.QuotaWindow{Label: "Credits", Unit: usage.UnitCount, Used: 12.5}, "Credits 12.5"},
		{"zero total", usage.QuotaWindow{Label: "Credits", Unit: usage.UnitCount, Used: 0, Total: ptr(0.0)}, "Credits 0/0"},
		{"no reset", usage.QuotaWindow{Label: "Quota", Unit: usage.UnitPercentage, Used: 21}, "Quota 21%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, stale := range []bool{false, true} {
				u := usage.Usage{QuotaWindows: []usage.QuotaWindow{tt.window}}
				suffix := ""
				if stale {
					u.Stale = &usage.StaleInfo{Code: usage.CodeUnavailable}
					suffix = " · stale"
				}
				var out bytes.Buffer
				usage.RenderText(&out, "Fake", u, now, loc)
				want := "Fake · " + tt.want + suffix + "\n"
				if out.String() != want {
					t.Errorf("text = %q; want %q", out.String(), want)
				}
			}
		})
	}
}

func TestRenderTextNoWindows(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	usage.RenderText(&out, "Fake", usage.Usage{}, time.Now(), time.UTC)
	if out.String() != "Fake · no quota windows\n" {
		t.Errorf("text = %q", out.String())
	}
}
