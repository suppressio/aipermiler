package usage

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"time"
)

// RenderText writes human-readable usage; text is not a stable client contract.
func RenderText(w io.Writer, displayName string, u Usage, now time.Time, loc *time.Location) {
	stale := ""
	if u.Stale != nil {
		stale = " · stale"
	}
	if len(u.QuotaWindows) == 0 {
		_, _ = fmt.Fprintf(w, "%s · no quota windows%s\n", displayName, stale)
	}
	for _, q := range u.QuotaWindows {
		short := q.Label
		if q.PeriodLength != nil {
			switch q.PeriodType {
			case PeriodRollingHours:
				short = number(*q.PeriodLength) + "h"
			case PeriodRollingDays:
				short = number(*q.PeriodLength) + "d"
			case PeriodBillingCycle:
			}
		}
		amount := fmt.Sprintf("%.0f%%", math.Round(q.Used))
		if q.Unit == UnitCount {
			amount = number(q.Used)
			if q.Total != nil {
				amount += "/" + number(*q.Total)
				if *q.Total > 0 {
					amount += fmt.Sprintf(" (%.0f%%)", math.Round(q.Used / *q.Total * 100))
				}
			}
		}
		reset := ""
		if q.ResetsAt != nil {
			layout := "2006-01-02 15:04"
			delta := q.ResetsAt.Sub(now)
			if delta >= 0 && delta < 24*time.Hour {
				layout = "15:04"
			}
			reset = " · resets " + q.ResetsAt.In(loc).Format(layout)
		}
		_, _ = fmt.Fprintf(w, "%s · %s %s%s%s\n", displayName, short, amount, reset, stale)
	}
}

func number(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
