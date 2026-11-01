package usage_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

func TestUsageNullableFieldsAndUTC(t *testing.T) {
	t.Parallel()
	local := time.Date(2026, time.October, 31, 23, 10, 0, 987654321, time.FixedZone("Test", 2*60*60))
	tests := []struct {
		name  string
		value usage.Usage
		want  string
	}{
		{"null fields", usage.Usage{QuotaWindows: []usage.QuotaWindow{{ID: "unknown", Label: "Unknown", PeriodType: usage.PeriodBillingCycle, Unit: usage.UnitCount}}}, `{"planTier":null,"subscriptionRenewsAt":null,"quotaWindows":[{"id":"unknown","label":"Unknown","periodType":"billing-cycle","periodLength":null,"unit":"count","used":0,"total":null,"resetsAt":null}]}`},
		{"UTC dates", usage.Usage{SubscriptionRenewsAt: &local, QuotaWindows: []usage.QuotaWindow{{ID: "reset", ResetsAt: &local}}}, `{"planTier":null,"subscriptionRenewsAt":"2026-10-31T21:10:00Z","quotaWindows":[{"id":"reset","label":"","periodType":"","periodLength":null,"unit":"","used":0,"total":null,"resetsAt":"2026-10-31T21:10:00Z"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("JSON = %s; want %s", got, tt.want)
			}
		})
	}
	var out bytes.Buffer
	if err := usage.WriteSuccess(&out, "fake", usage.Usage{}, local); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"fetchedAt":"2026-10-31T21:10:00Z"`) {
		t.Errorf("timestamp = %s", out.String())
	}
}
