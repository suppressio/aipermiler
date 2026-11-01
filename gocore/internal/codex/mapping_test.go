package codex_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/codex"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

func ptr[T any](value T) *T { return &value }
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func wantWindow(days bool, used float64, reset *time.Time) usage.QuotaWindow {
	w := usage.QuotaWindow{ID: "codex-5h", Label: "5-hour limit", PeriodType: usage.PeriodRollingHours, PeriodLength: ptr(5.0), Unit: usage.UnitPercentage, Used: used, Total: ptr(100.0), ResetsAt: reset}
	if days {
		w.ID, w.Label, w.PeriodType, w.PeriodLength = "codex-7d", "7-day limit", usage.PeriodRollingDays, ptr(7.0)
	}
	return w
}
func assertDrift(t *testing.T, err error, data []byte) {
	t.Helper()
	var e *usage.Error
	if !errors.As(err, &e) || e.Code != usage.CodeFormatDrift || e.Endpoint != "codex app-server account/rateLimits/read" || e.Message != "Codex: no quota window recognized" {
		t.Fatalf("drift error = %#v", err)
	}
	var decoded any
	if decodeErr := json.Unmarshal(data, &decoded); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if !reflect.DeepEqual(e.Shape, usage.ExtractShape(decoded)) {
		t.Errorf("shape = %#v", e.Shape)
	}
	encoded, marshalErr := json.Marshal(e.Shape)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, value := range []string{"plus", "synthetic-value-never-in-shape", "1793484600", "123.45", "synthetic-credit"} {
		if strings.Contains(string(encoded), value) {
			t.Errorf("shape contains value %q: %s", value, encoded)
		}
	}
}

// Fixtures follow codex 0.163.0's generated GetAccountRateLimitsResponse schema;
// unknown-shape deliberately violates its required rateLimits field. All values are synthetic.
func TestMappingFixtures(t *testing.T) {
	t.Parallel()
	five := wantWindow(false, 21, ptr(time.Unix(1793484600, 0).UTC()))
	week := wantWindow(true, 42, ptr(time.Unix(1794085800, 0).UTC()))
	tests := []struct {
		name    string
		plan    *string
		windows []usage.QuotaWindow
	}{
		{"plus", ptr("plus"), []usage.QuotaWindow{five, week}},
		{"multi-bucket", ptr("business"), []usage.QuotaWindow{five, week}},
		{"weekly-only", ptr("plus"), []usage.QuotaWindow{week}},
		{"odd-durations", ptr("plus"), []usage.QuotaWindow{five}},
		{"no-windows", nil, nil}, {"unknown-shape", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := fixture(t, tt.name)
			got, err := codex.MapForTest(data)
			if tt.windows == nil {
				assertDrift(t, err, data)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := usage.Usage{PlanTier: tt.plan, QuotaWindows: tt.windows}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("usage = %#v; want %#v", got, want)
			}
		})
	}
}

func TestMappingEdges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, data string
		windows    []usage.QuotaWindow
	}{
		{"not clamped and duplicate first wins", `{"rateLimits":{"primary":{"usedPercent":125.5,"windowDurationMins":300,"resetsAt":null},"secondary":{"usedPercent":1,"windowDurationMins":300,"resetsAt":123}}}`, []usage.QuotaWindow{wantWindow(false, 125.5, nil)}},
		{"missing used skipped", `{"rateLimits":{"primary":{"windowDurationMins":300},"secondary":{"usedPercent":0,"windowDurationMins":10080,"resetsAt":0}}}`, []usage.QuotaWindow{wantWindow(true, 0, nil)}},
		{"2000 minutes skipped", `{"rateLimits":{"primary":{"usedPercent":10,"windowDurationMins":2000},"secondary":{"usedPercent":21,"windowDurationMins":300,"resetsAt":-1}}}`, []usage.QuotaWindow{wantWindow(false, 21, nil)}},
		{"missing duration", `{"rateLimits":{"primary":{"usedPercent":0}}}`, nil},
		{"negative usage", `{"rateLimits":{"primary":{"usedPercent":-1,"windowDurationMins":300}}}`, nil},
		{"zero duration", `{"rateLimits":{"primary":{"usedPercent":0,"windowDurationMins":0}}}`, nil},
		{"negative duration", `{"rateLimits":{"primary":{"usedPercent":0,"windowDurationMins":-300}}}`, nil},
		{"wrong type", `{"rateLimits":{"primary":{"usedPercent":"21","windowDurationMins":300}}}`, nil},
		{"array", `[]`, nil}, {"scalar", `42`, nil}, {"null", `null`, nil},
		{"codex null has no fallback", `{"rateLimits":{"primary":{"usedPercent":21,"windowDurationMins":300}},"rateLimitsByLimitId":{"codex":null}}`, nil},
		{"missing legacy bucket", `{"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":21,"windowDurationMins":300}}}}`, nil},
		{"other buckets ignored", `{"rateLimits":{"primary":{"usedPercent":21,"windowDurationMins":300}},"rateLimitsByLimitId":{"synthetic-other":{"primary":"unexpected"}}}`, []usage.QuotaWindow{wantWindow(false, 21, nil)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := codex.MapForTest([]byte(tt.data))
			if tt.windows == nil {
				assertDrift(t, err, []byte(tt.data))
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, usage.Usage{QuotaWindows: tt.windows}) {
				t.Errorf("usage = %#v; want windows %#v", got, tt.windows)
			}
		})
	}
}
