package claude

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// creditsEndpoint names the /usage panel block in a drift report (#257).
const creditsEndpoint = "claude /usage panel (Usage credits)"

// markerCredits starts the "Usage credits" block of the /usage panel.
const markerCredits = "usagecredits"

// creditsBlock matches the block on normalized panel text (no white space, lower
// case), e.g. `usagecredits█████9%used$14.74/$150.00spent·resetsdec1(europe/rome)`
// (Windows 11, 2.1.319, #249; values replaced in docs and tests). The symbol before
// each amount is kept so a currency other than $ is told apart from a garbled block.
var creditsBlock = regexp.MustCompile(`usagecredits[^%]*?\d+(?:\.\d+)?%used([^\d/]+)(\d[\d,]*(?:\.\d+)?)/([^\d/]+)(\d[\d,]*(?:\.\d+)?)spent[^a-z]*resets([a-z]{3})(\d{1,2})\(`)

var months = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April,
	"may": time.May, "jun": time.June, "jul": time.July, "aug": time.August,
	"sep": time.September, "oct": time.October, "nov": time.November, "dec": time.December,
}

// mapCredits reads the "Usage credits" block of a plan billed through usage credits
// (#255) as the monthly `spend` window of v0.4.x: no block, or a currency other than
// $, keeps the usage_credits notice; a block that does not read is format drift.
func mapCredits(panel string, now time.Time) (usage.Usage, error) {
	notice := unavailableBecause(ReasonUsageCredits, "Claude Code is signed in on a plan billed through usage credits, which has no quota windows")
	if !strings.Contains(panel, markerCredits) {
		return usage.Usage{}, notice
	}
	// Only the last redraw of the block counts: an earlier complete one must not hide
	// a last one that does not read.
	m := creditsBlock.FindStringSubmatch(panel[strings.LastIndex(panel, markerCredits):])
	if m == nil {
		return usage.Usage{}, creditsDrift()
	}
	if m[1] != "$" || m[3] != "$" {
		return usage.Usage{}, notice
	}
	used, okUsed := amount(m[2])
	limit, okLimit := amount(m[4])
	month, okMonth := months[m[5]]
	day, err := strconv.Atoi(m[6])
	if !okUsed || !okLimit || limit <= 0 || !okMonth || err != nil {
		return usage.Usage{}, creditsDrift()
	}
	reset, ok := nextReset(month, day, now)
	if !ok {
		return usage.Usage{}, creditsDrift()
	}
	length := 1.0
	return usage.Usage{QuotaWindows: []usage.QuotaWindow{{
		ID: "spend", Label: "Spend limit", PeriodType: usage.PeriodBillingCycle, PeriodLength: &length,
		Unit: usage.UnitCount, Used: used, Total: &limit, ResetsAt: &reset,
	}}}, nil
}

// amount reads `1,234.56`.
func amount(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	return v, err == nil && v >= 0
}

// nextReset is the next occurrence of month/day after now, at local midnight: the
// panel gives no year or time, and the zone it shows is this machine's (inferred).
// A date that exists in no year (apr 31) is false; feb 29 waits for a leap year.
func nextReset(month time.Month, day int, now time.Time) (time.Time, bool) {
	year := now.In(time.Local).Year()
	for y := year; y <= year+8; y++ {
		reset := time.Date(y, month, day, 0, 0, 0, 0, time.Local)
		if reset.Month() == month && reset.Day() == day && reset.After(now) {
			return reset.UTC(), true
		}
	}
	return time.Time{}, false
}

func creditsDrift() *usage.Error {
	return &usage.Error{Code: usage.CodeFormatDrift, Message: "Claude: the Usage credits block of /usage was not recognized", Endpoint: creditsEndpoint}
}
