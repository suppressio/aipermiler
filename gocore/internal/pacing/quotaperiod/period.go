// Package quotaperiod defines quota windows and their calendar and working time.
// All dates use the caller's non-nil location; no function reads the clock.
package quotaperiod

import (
	"fmt"
	"time"
)

// Kind selects the time base of a quota window (PACING.md §1).
type Kind string

// Supported quota window kinds.
const (
	RollingHours Kind = "rolling-hours"
	RollingDays  Kind = "rolling-days"
	BillingCycle Kind = "billing-cycle"
)

// Window contains only temporal provider data. Length is in hours, days or
// calendar months according to Kind; zero means an unknown length.
type Window struct {
	Kind     Kind
	Length   int
	ResetsAt time.Time
}

// Period is [Start, End). A zero time represents a missing bound.
// Billing-cycle bounds are local midnights; rolling bounds retain their time.
type Period struct {
	Kind       Kind
	Start, End time.Time
}

// ResolvePeriod gives rolling windows only their own reset. Billing cycles use
// window reset, provider renewal, then the manual rule, in that order (§3.16).
func ResolvePeriod(window Window, providerRenewal time.Time, manual RenewalRule, now time.Time, loc *time.Location) (Period, error) {
	p := Period{Kind: window.Kind, End: window.ResetsAt}
	switch window.Kind {
	case RollingHours, RollingDays:
		if !p.End.IsZero() && window.Length > 0 {
			span := time.Hour
			if window.Kind == RollingDays {
				span *= 24
			}
			p.Start = p.End.Add(-time.Duration(window.Length) * span)
		}
	case BillingCycle:
		if p.End.IsZero() {
			p.End = providerRenewal
		}
		if p.End.IsZero() {
			var err error
			p.End, err = ResolveRenewalDate(manual, now, loc)
			if err != nil {
				return Period{}, err
			}
		}
		p.End = localDay(p.End, loc)
		if window.Length > 0 {
			p.Start = p.End.AddDate(0, -window.Length, 0)
		}
	default:
		return Period{}, fmt.Errorf("unsupported window kind %q", window.Kind)
	}
	return p, nil
}

// HoursUntilReset returns real remaining hours, floored at zero. The boolean
// is false when the reset is missing, including for a rolling window (§5 E6).
func (p Period) HoursUntilReset(now time.Time) (float64, bool) {
	if p.End.IsZero() {
		return 0, false
	}
	return max(0, p.End.Sub(now).Hours()), true
}

// DaysUntilReset uses fractional 24-hour days for rolling windows and local
// calendar days for billing cycles, even when their start is unknown (§5 E9).
func (p Period) DaysUntilReset(now time.Time, loc *time.Location) (float64, bool) {
	hours, ok := p.HoursUntilReset(now)
	if !ok || p.Kind != BillingCycle {
		return hours / 24, ok
	}
	// Compare the local date fields, independently of each timestamp's offset.
	y, m, d := now.In(loc).Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	y, m, d = p.End.In(loc).Date()
	end := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return max(0, end.Sub(start).Hours()/24), true
}

func localDay(instant time.Time, loc *time.Location) time.Time {
	y, m, d := instant.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

func monthDay(year int, month time.Month, day int, loc *time.Location) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, loc)
	return time.Date(last.Year(), last.Month(), min(day, last.Day()), 0, 0, 0, 0, loc)
}
