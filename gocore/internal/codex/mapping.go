package codex

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const endpoint = "codex app-server account/rateLimits/read"

type rateWindow struct {
	UsedPercent *float64 `json:"usedPercent"`
	Duration    *int64   `json:"windowDurationMins"`
	ResetsAt    *int64   `json:"resetsAt"`
}

type bucket struct {
	PlanType  *string     `json:"planType"`
	Primary   *rateWindow `json:"primary"`
	Secondary *rateWindow `json:"secondary"`
}

func drift(decoded any) *usage.Error {
	return &usage.Error{Code: usage.CodeFormatDrift, Message: "Codex: no quota window recognized", Endpoint: endpoint, Shape: usage.ExtractShape(decoded)}
}

func mapUsage(data json.RawMessage) (usage.Usage, error) {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return usage.Usage{}, drift(nil)
	}
	var response struct {
		RateLimits *bucket                    `json:"rateLimits"`
		ByLimitID  map[string]json.RawMessage `json:"rateLimitsByLimitId"`
	}
	if err := json.Unmarshal(data, &response); err != nil || response.RateLimits == nil {
		return usage.Usage{}, drift(decoded)
	}
	selected := response.RateLimits
	if raw, ok := response.ByLimitID["codex"]; ok {
		// Decode only the codex bucket; other buckets have unverified plan semantics.
		selected = nil
		if err := json.Unmarshal(raw, &selected); err != nil || selected == nil {
			return usage.Usage{}, drift(decoded)
		}
	}
	result := usage.Usage{PlanTier: selected.PlanType}
	for _, w := range []*rateWindow{selected.Primary, selected.Secondary} {
		window, ok := mapWindow(w)
		if !ok {
			continue
		}
		duplicate := false
		for _, existing := range result.QuotaWindows {
			if existing.ID == window.ID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result.QuotaWindows = append(result.QuotaWindows, window)
		}
	}
	if len(result.QuotaWindows) == 0 {
		return usage.Usage{}, drift(decoded)
	}
	return result, nil
}

func mapWindow(w *rateWindow) (usage.QuotaWindow, bool) {
	if w == nil || w.UsedPercent == nil || *w.UsedPercent < 0 || w.Duration == nil || *w.Duration <= 0 {
		return usage.QuotaWindow{}, false
	}
	minutes := *w.Duration
	var length int64
	period, suffix, label := usage.PeriodRollingHours, "h", "hour"
	switch {
	case minutes%1440 == 0:
		length, period, suffix, label = minutes/1440, usage.PeriodRollingDays, "d", "day"
	case minutes < 1440 && minutes%60 == 0:
		length = minutes / 60
	default:
		return usage.QuotaWindow{}, false
	}
	n, total := float64(length), 100.0
	result := usage.QuotaWindow{ID: fmt.Sprintf("codex-%d%s", length, suffix), Label: fmt.Sprintf("%d-%s limit", length, label), PeriodType: period, PeriodLength: &n, Unit: usage.UnitPercentage, Used: *w.UsedPercent, Total: &total}
	if w.ResetsAt != nil && *w.ResetsAt > 0 {
		reset := time.Unix(*w.ResetsAt, 0).UTC()
		// The JSON contract requires an RFC 3339 year.
		if reset.Year() >= 0 && reset.Year() <= 9999 {
			result.ResetsAt = &reset
		}
	}
	return result, true
}
