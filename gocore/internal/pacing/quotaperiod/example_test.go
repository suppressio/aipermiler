package quotaperiod_test

import (
	"fmt"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
)

func ExamplePeriod_Units() {
	// HYP#78: seven days starting at 23:00, with no work schedule.
	loc := time.FixedZone("local", 2*60*60)
	p := quotaperiod.Period{
		Kind:  quotaperiod.RollingDays,
		Start: time.Date(2026, time.October, 14, 23, 0, 0, 0, loc),
		End:   time.Date(2026, time.October, 21, 23, 0, 0, 0, loc),
	}
	now := time.Date(2026, time.October, 17, 18, 38, 0, 0, loc)
	units, ok := p.Units(now, loc, quotaperiod.Schedule{HoursPerDay: 8}, nil)
	fmt.Printf("Available: %t; elapsed: %.2f days; remaining: %.2f days\n", ok, units.Elapsed, units.Remaining)
	// Output: Available: true; elapsed: 2.82 days; remaining: 4.18 days
}

func ExamplePeriod_Units_scheduled() {
	// HYP#78 with weekdays enabled: Saturday contributes no working units.
	loc := time.FixedZone("local", 2*60*60)
	p := quotaperiod.Period{
		Kind:  quotaperiod.RollingDays,
		Start: time.Date(2026, time.October, 14, 23, 0, 0, 0, loc),
		End:   time.Date(2026, time.October, 21, 23, 0, 0, 0, loc),
	}
	s := quotaperiod.Schedule{Enabled: true, Days: [7]float64{0, 1, 1, 1, 1, 1, 0}, HoursPerDay: 8}
	now := time.Date(2026, time.October, 17, 18, 38, 0, 0, loc)
	units, _ := p.Units(now, loc, s, nil)
	fmt.Printf("Total: %.2f; elapsed: %.2f; remaining: %.2f working units\n", units.Total, units.Elapsed, units.Remaining)
	// Output: Total: 5.00; elapsed: 2.04; remaining: 2.96 working units
}
