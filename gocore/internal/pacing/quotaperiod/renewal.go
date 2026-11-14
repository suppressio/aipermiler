package quotaperiod

import (
	"fmt"
	"time"
)

// RenewalRule supports only Type "dayOfMonth" with Day in [1, 31].
type RenewalRule struct {
	Type string
	Day  int
}

// ResolveRenewalDate returns the next local midnight strictly after now,
// clamping the requested day to the month's last day (PACING.md §3.16).
func ResolveRenewalDate(rule RenewalRule, now time.Time, loc *time.Location) (time.Time, error) {
	if rule.Type != "dayOfMonth" {
		return time.Time{}, fmt.Errorf("renewal rule type %q not supported", rule.Type)
	}
	if rule.Day < 1 || rule.Day > 31 {
		return time.Time{}, fmt.Errorf("renewal day must be between 1 and 31: %d", rule.Day)
	}
	y, m, _ := now.In(loc).Date()
	candidate := monthDay(y, m, rule.Day, loc)
	if !candidate.After(now) {
		candidate = monthDay(y, m+1, rule.Day, loc)
	}
	return candidate, nil
}
