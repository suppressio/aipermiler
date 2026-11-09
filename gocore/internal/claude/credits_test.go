package claude_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/claude"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

func TestCredits(t *testing.T) {
	t.Parallel()
	nov := time.Date(2026, time.November, 9, 12, 0, 0, 0, time.Local)
	dec := time.Date(2026, time.December, 15, 12, 0, 0, 0, time.Local)
	midnight := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.Local).UTC() }
	block := func(rest string) string {
		return "sessiontotalcost:$0.0000usagecredits█████░░░░9%used" + rest + "esctocancel"
	}
	tests := []struct {
		name        string
		panel       string
		now         time.Time
		code        usage.Code
		reason      string
		used, total float64
		reset       time.Time
	}{
		{"normal", block("$1.00/$10.00spent·resetsdec1(europe/rome)"), nov, "", "", 1, 10, midnight(2026, time.December, 1)},
		{"thousands separator", block("$1,234.50/$2,000spent·resetsdec1(utc)"), nov, "", "", 1234.5, 2000, midnight(2026, time.December, 1)},
		{"december to january", block("$1.00/$10.00spent·resetsjan1(utc)"), dec, "", "", 1, 10, midnight(2027, time.January, 1)},
		{"date already passed this year", block("$1.00/$10.00spent·resetsnov1(utc)"), nov, "", "", 1, 10, midnight(2027, time.November, 1)},
		{"last redraw wins", block("$1.00/$10.00spent·resetsdec1(utc)") + block("$2.00/$10.00spent·resetsdec1(utc)"), nov, "", "", 2, 10, midnight(2026, time.December, 1)},
		{"other currency", block("€1.00/€10.00spent·resetsdec1(europe/rome)"), nov, usage.CodeUnavailable, claude.ReasonUsageCredits, 0, 0, time.Time{}},
		{"no block", "sessiontotalcost:$0.0000currentsession12%used", nov, usage.CodeUnavailable, claude.ReasonUsageCredits, 0, 0, time.Time{}},
		{"panel not reached", "", nov, usage.CodeUnavailable, claude.ReasonUsageCredits, 0, 0, time.Time{}},
		{"garbled block", block("creditsunavailable"), nov, usage.CodeFormatDrift, "", 0, 0, time.Time{}},
		{"zero limit", block("$0.00/$0.00spent·resetsdec1(utc)"), nov, usage.CodeFormatDrift, "", 0, 0, time.Time{}},
		{"unknown month", block("$1.00/$10.00spent·resetsxyz1(utc)"), nov, usage.CodeFormatDrift, "", 0, 0, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := claude.CreditsForTest(tt.panel, tt.now)
			if tt.code != "" {
				var uerr *usage.Error
				if !errors.As(err, &uerr) || uerr.Code != tt.code || uerr.Reason != tt.reason {
					t.Fatalf("err = %v, want %s/%q", err, tt.code, tt.reason)
				}
				if tt.code == usage.CodeFormatDrift && (uerr.Endpoint != "claude /usage panel (Usage credits)" || uerr.Shape != nil) {
					t.Errorf("drift endpoint = %q, shape = %v", uerr.Endpoint, uerr.Shape)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got.QuotaWindows) != 1 {
				t.Fatalf("windows = %d", len(got.QuotaWindows))
			}
			w := got.QuotaWindows[0]
			if w.ID != "spend" || w.Unit != usage.UnitCount || w.PeriodType != usage.PeriodBillingCycle || w.PeriodLength == nil || *w.PeriodLength != 1 {
				t.Errorf("window = %+v", w)
			}
			if w.Used != tt.used || w.Total == nil || *w.Total != tt.total {
				t.Errorf("used/total = %v/%v, want %v/%v", w.Used, w.Total, tt.used, tt.total)
			}
			if w.ResetsAt == nil || !w.ResetsAt.Equal(tt.reset) {
				t.Errorf("reset = %v, want %v", w.ResetsAt, tt.reset)
			}
		})
	}
}

// The panel kept by the driver is the normalized screen: cursor moves that drop the
// spaces still read.
func TestCreditsFromDrivenPanel(t *testing.T) {
	t.Parallel()
	f := newFake(screenPrompt, func(f *fake, key string) { showPanel(f, key, screenCreditsPanel) })
	panel, err := claude.PanelForTest(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := claude.CreditsForTest(panel, time.Date(2026, time.November, 9, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.QuotaWindows) != 1 || got.QuotaWindows[0].Used != 1 || *got.QuotaWindows[0].Total != 10 {
		t.Fatalf("got %+v", got)
	}
}
