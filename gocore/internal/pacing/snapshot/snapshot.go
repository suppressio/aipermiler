// Package snapshot builds the whole pacing snapshot of one account from the
// stored history, the provider response, the settings and now, and returns the
// history update to persist (docs/PACING.md §7, #75). It is composition only:
// every quantity comes from the pacing packages (quotaperiod, history, pace,
// outlook, advice, insights); nothing here computes pacing of its own.
//
// Values are exact (I-4): nothing is rounded here, the client rounds when it
// shows a value. Undefined values are reported with an explicit flag or a nil
// pointer, never as 0 (I-5). It is pure: no I/O and no hidden clock.
package snapshot

import (
	"cmp"
	"maps"
	"slices"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/advice"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/insights"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/outlook"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/pace"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
)

// Window is one quota window of the provider response (contract v1).
type Window struct {
	ID   string
	Kind quotaperiod.Kind
	// Length is in hours, days or months according to Kind; 0 is unknown.
	Length int
	Unit   pace.Unit
	Used   float64
	// Total is the quota of a count window; 0 or less is none.
	Total float64
	// ResetsAt is zero when the provider gives no reset.
	ResetsAt time.Time
}

// Response is a provider read of one account.
type Response struct {
	Windows []Window
	// SubscriptionRenewsAt is zero when the provider does not report it.
	SubscriptionRenewsAt time.Time
	// FetchedAt is when the values were read; Fresh is false for last known
	// values shown again after a failed or skipped fetch: they are never
	// recorded (D4, I-12).
	FetchedAt time.Time
	Fresh     bool
}

// Settings are the account's settings and the view's choices.
type Settings struct {
	Location *time.Location
	Schedule quotaperiod.Schedule
	// Renewal is the manual renewal day, the last fallback of a billing cycle.
	Renewal quotaperiod.RenewalRule
	// RetentionDays of the daily points; 0 or less is defaultRetentionDays.
	RetentionDays int
	// ChartDays is 7 or 30: the chart and the rating span (D-3.22, D-3.23).
	ChartDays int
	// FirstSession is the start of today's first local Claude Code session,
	// zero when unknown (D-3.3).
	FirstSession time.Time
	// LocalTokens are the local Claude Code output tokens per local day, for
	// the local insights (§3.15); nil when the account does not count them
	// (opt-in): no yield and no cause then.
	LocalTokens []insights.Tokens
	// TipDraw in [0, 1) picks one tip among the candidates (§3.14).
	TipDraw float64
}

// Input is everything the snapshot is built from. History is per window id
// and is never modified.
type Input struct {
	History  map[string]history.State
	Response Response
	Settings Settings
	Now      time.Time
}

// Result is the snapshot and the history to persist in place of the input one.
type Result struct {
	Account Account
	History map[string]history.State
}

// Account is the snapshot of every window, in the response order.
type Account struct {
	Windows []WindowSnapshot
	// Critical is the index of the window the widget opens on, -1 without
	// windows (§3.10, V-12).
	Critical int
}

// WindowSnapshot holds every reading of one window. A nil pointer or a false
// Has flag is an undefined value, hidden by the client.
type WindowSnapshot struct {
	ID string
	// Used is x (D-3.1), not clamped; HasUsed is false without a utilization
	// (a count window without total, E14): only the readings that do not
	// depend on it stay defined (days and working units to the reset, chart).
	Used      float64
	HasUsed   bool
	Remaining float64
	// Period is zero when it cannot be resolved.
	Period quotaperiod.Period
	// Units are the working units of a day-based window with known bounds.
	Units *quotaperiod.WorkingUnits
	// DaysToReset are calendar days for billing cycles, 24 h days otherwise.
	DaysToReset    float64
	HasDaysToReset bool
	// UnitsToReset is R, defined with a known reset even without a period
	// start (the summary needs the start, R does not).
	UnitsToReset    float64
	HasUnitsToReset bool
	Efficiency      float64
	HasEfficiency   bool
	// Baseline is today's x0 (§7).
	Baseline    float64
	HasBaseline bool
	// UsedToday is defined with a baseline (D-3.16).
	UsedToday    float64
	HasUsedToday bool
	// TodayBudget is B (D-3.15); PaceAlert is D-3.17.
	TodayBudget    float64
	HasTodayBudget bool
	PaceAlert      bool
	Redistribution *outlook.Redistribution
	Summary        *outlook.Summary
	Hourly         *outlook.Hourly
	// InstantPace is v (D-3.11).
	InstantPace    float64
	HasInstantPace bool
	// Trend is the instant trend (D-3.28), only on a fresh read.
	Trend    pace.Trend
	HasTrend bool
	// Ceiling is C (D-3.12), Target is T (D-3.13).
	Ceiling    float64
	HasCeiling bool
	Target     float64
	HasTarget  bool
	// Gauge is g(v, T) (D-3.14), defined with a target; no measured pace is 0.
	Gauge     float64
	HasGauge  bool
	Judgement outlook.Judgement
	Chart     []advice.Slot
	Stats     advice.Stats
	Rating    *advice.Rating
	Tip       *advice.Tip
	// Yield (D-3.26) and Cause (D-3.27) are the local insights of a day-based
	// window, nil without local tokens or below their thresholds (I-5).
	Yield *insights.Yield
	Cause *insights.Cause
}

// defaultRetentionDays matches the store's default (internal/store).
const defaultRetentionDays = 90

// Build composes the snapshot (§3, §7). The history of each window with a
// utilization goes through history.Record, its single writer (I-12); the rest
// of the input history is copied unchanged.
func Build(in Input) Result {
	next := maps.Clone(in.History)
	if next == nil {
		next = map[string]history.State{}
	}
	account := Account{Windows: make([]WindowSnapshot, 0, len(in.Response.Windows)), Critical: -1}
	for _, w := range in.Response.Windows {
		snap, state, recorded := buildWindow(in, w)
		if recorded {
			next[w.ID] = state
		}
		account.Windows = append(account.Windows, snap)
	}
	account.Critical = Critical(account.Windows)
	return Result{Account: account, History: next}
}

func buildWindow(in Input, w Window) (WindowSnapshot, history.State, bool) {
	snap := WindowSnapshot{ID: w.ID}
	x, ok := pace.Used(w.Used, w.Total, w.Unit)
	if !ok {
		snap.Judgement = outlook.Judgement{Verdict: outlook.NoPacing}
		withoutUtilization(&snap, in, w)
		return snap, history.State{}, false
	}
	snap.Used, snap.HasUsed, snap.Remaining = x, true, pace.Remaining(x)

	set, loc, now := in.Settings, in.Settings.Location, in.Now
	period, err := quotaperiod.ResolvePeriod(
		quotaperiod.Window{Kind: w.Kind, Length: w.Length, ResetsAt: w.ResetsAt},
		in.Response.SubscriptionRenewsAt, set.Renewal, now, loc)
	if err != nil {
		period = quotaperiod.Period{Kind: w.Kind}
	}
	snap.Period = period

	state := record(in, w, x)

	today := startOfDay(now, loc)
	for _, point := range state.DailyUsage {
		if point.Day.Equal(today) {
			snap.Baseline, snap.HasBaseline = point.DayStartUsed, true
		}
	}
	if snap.HasBaseline {
		snap.UsedToday, snap.HasUsedToday = outlook.UsedToday(x, snap.Baseline), true
	}
	snap.InstantPace, snap.HasInstantPace = pace.InstantPace(state.RecentSamples, now)
	if in.Response.Fresh {
		snap.Trend, snap.HasTrend = pace.InstantTrend(state.RecentSamples, now)
	}
	// Forward-looking values, days to reset included, need a reset still
	// ahead (I-6, D5, E5): a passed reset is not "today".
	resetAhead := !period.End.IsZero() && period.End.After(now)
	if resetAhead {
		snap.DaysToReset, snap.HasDaysToReset = period.DaysUntilReset(now, loc)
	}

	if w.Kind == quotaperiod.RollingHours {
		rollingHours(&snap, period, w.Length, now)
		snap.Judgement = outlook.Judge(outlook.VerdictInput{Used: x, Hourly: snap.Hourly})
		return snap, state, true
	}

	var baseline *float64
	if snap.HasBaseline {
		baseline = &snap.Baseline
	}
	var worked *float64
	if span, ok := pace.TodayActivity(state.RecentSamples, baseline, set.FirstSession, now, loc); ok {
		hours := span.Hours()
		worked = &hours
	}
	units, hasUnits := period.Units(now, loc, set.Schedule, worked)
	var totalUnits float64
	if hasUnits {
		snap.Units, totalUnits = &units, units.Total
		snap.Efficiency, snap.HasEfficiency = pace.Efficiency(x, units.Total, units.Elapsed)
	}
	if resetAhead {
		snap.UnitsToReset, snap.HasUnitsToReset = unitsToReset(period, units, hasUnits, now, loc, set.Schedule, worked)
	}
	if hasUnits && resetAhead {
		dayBased(&snap, state, units, period, set, now)
	}
	snap.Judgement = outlook.Judge(outlook.VerdictInput{Used: x, Redistribution: snap.Redistribution, Summary: snap.Summary})

	upcoming := 2
	if set.ChartDays == 30 {
		upcoming = 5
	}
	snap.Chart = advice.Chart(advice.ChartInput{
		Points: state.DailyUsage, Schedule: set.Schedule, Start: period.Start, End: period.End,
		TotalUnits: totalUnits, Now: now, Location: loc, PastDays: set.ChartDays, UpcomingDays: upcoming,
		TodayBudget: snap.TodayBudget, HasTodayBudget: snap.HasTodayBudget,
		PerUnit: perUnit(snap.Redistribution), HasPerUnit: snap.Redistribution != nil,
	})
	snap.Stats = advice.Summarize(snap.Chart, now, loc)
	if hasUnits {
		if rating, ok := advice.Rate(state.DailyUsage, set.Schedule, totalUnits, now, loc, set.ChartDays); ok {
			snap.Rating = &rating
		}
	}
	if set.LocalTokens != nil {
		localInsights(&snap, state, set, totalUnits)
	}
	snap.Tip = tip(snap, set.TipDraw)
	return snap, state, true
}

// withoutUtilization fills what does not depend on the used quota: the days
// and working units to the reset and the chart over the history already kept
// (E14). The percentage metrics stay undefined and nothing is recorded.
func withoutUtilization(snap *WindowSnapshot, in Input, w Window) {
	set, loc, now := in.Settings, in.Settings.Location, in.Now
	period, err := quotaperiod.ResolvePeriod(
		quotaperiod.Window{Kind: w.Kind, Length: w.Length, ResetsAt: w.ResetsAt},
		in.Response.SubscriptionRenewsAt, set.Renewal, now, loc)
	if err != nil {
		period = quotaperiod.Period{Kind: w.Kind}
	}
	snap.Period = period
	if w.Kind == quotaperiod.RollingHours {
		return // read in hours, which needs the used quota
	}
	state := in.History[w.ID]
	resetAhead := !period.End.IsZero() && period.End.After(now)
	// No baseline without a utilization, as in TypeScript: only samples stored
	// before the window lost its total can still mark today's work (D-2.4).
	var worked *float64
	if span, ok := pace.TodayActivity(state.RecentSamples, nil, set.FirstSession, now, loc); ok {
		hours := span.Hours()
		worked = &hours
	}
	units, hasUnits := period.Units(now, loc, set.Schedule, worked)
	var totalUnits float64
	if hasUnits {
		snap.Units, totalUnits = &units, units.Total
	}
	if resetAhead {
		snap.DaysToReset, snap.HasDaysToReset = period.DaysUntilReset(now, loc)
		snap.UnitsToReset, snap.HasUnitsToReset = unitsToReset(period, units, hasUnits, now, loc, set.Schedule, worked)
	}
	upcoming := 2
	if set.ChartDays == 30 {
		upcoming = 5
	}
	snap.Chart = advice.Chart(advice.ChartInput{
		Points: state.DailyUsage, Schedule: set.Schedule, Start: period.Start, End: period.End,
		TotalUnits: totalUnits, Now: now, Location: loc, PastDays: set.ChartDays, UpcomingDays: upcoming,
	})
	snap.Stats = advice.Summarize(snap.Chart, now, loc)
}

// unitsToReset is R: the units' own remaining part with a known period start,
// else from the reset and the schedule alone (E9). The caller checks that the
// reset is still ahead.
func unitsToReset(period quotaperiod.Period, units quotaperiod.WorkingUnits, hasUnits bool,
	now time.Time, loc *time.Location, schedule quotaperiod.Schedule, worked *float64,
) (float64, bool) {
	if hasUnits {
		return units.Remaining, true
	}
	return period.UnitsToReset(now, loc, schedule, worked)
}

// localInsights crosses the window's daily deltas with the local tokens
// (§3.15), over the last ChartDays+1 daily points as Electron's analysis
// window: an older day would meet a partial token count. Rolling-hours
// windows never get here: a "daily" delta there spans several resets and
// measures nothing.
func localInsights(snap *WindowSnapshot, state history.State, set Settings, totalUnits float64) {
	points := slices.SortedStableFunc(slices.Values(state.DailyUsage), func(a, b history.DailyPoint) int { return a.Day.Compare(b.Day) })
	span := set.ChartDays
	if span <= 0 {
		span = 7 // the service's default chart
	}
	if keep := span + 1; len(points) > keep {
		points = points[len(points)-keep:]
	}
	daily := advice.Deltas(points, set.Schedule, totalUnits, set.Location)
	deltas := make([]insights.Delta, 0, len(daily))
	for _, d := range daily {
		deltas = append(deltas, insights.Delta{Date: d.Day.In(set.Location).Format(time.DateOnly), Delta: d.Delta, Known: d.HasDelta})
	}
	if y, ok := insights.TokenYield(set.LocalTokens, deltas); ok {
		snap.Yield = &y
	}
	if c, ok := insights.ConsumptionCause(set.LocalTokens, deltas); ok {
		snap.Cause = &c
	}
}

// rollingHours fills the reading in hours of a rolling-hours window (§3.9).
func rollingHours(snap *WindowSnapshot, period quotaperiod.Period, length int, now time.Time) {
	hourly, ok := outlook.HourlyOutlook(snap.Used, snap.InstantPace, snap.HasInstantPace, period.End, !period.End.IsZero(), now)
	if ok {
		snap.Hourly = &hourly
	}
	hoursLeft, _ := period.HoursUntilReset(now)
	snap.Ceiling, snap.HasCeiling = pace.Ceiling(snap.Remaining, 0, hoursLeft)
	ideal, hasIdeal := pace.RollingIdealHourly(length)
	gauge(snap, ideal, hasIdeal)
}

// dayBased fills the readings of a day-based window with known bounds and a
// reset still ahead (§3.2 to §3.8, §3.13).
func dayBased(snap *WindowSnapshot, state history.State, units quotaperiod.WorkingUnits, period quotaperiod.Period, set Settings, now time.Time) {
	loc := set.Location
	recent, hasRecent := pace.RecentPace(state.DailyUsage, set.Schedule, period, now, loc)
	summary := outlook.Summarize(snap.Used, units.Elapsed, units.Remaining, recent, hasRecent)
	snap.Summary = &summary
	if r, ok := outlook.Redistribute(snap.Remaining, units.FromToday, units.Total); ok {
		snap.Redistribution = &r
	}
	inPeriod := !now.Before(period.Start)
	snap.TodayBudget, snap.HasTodayBudget = outlook.TodayBudget(snap.Baseline, snap.HasBaseline && inPeriod,
		units.FromToday, set.Schedule.DayUnit(now, loc))
	snap.PaceAlert = snap.HasTodayBudget && outlook.PaceAlert(snap.UsedToday, snap.TodayBudget)

	hoursLeft, _ := period.HoursUntilReset(now)
	snap.Ceiling, snap.HasCeiling = pace.Ceiling(snap.Remaining, units.RemainingHours, hoursLeft)
	share, _ := pace.EvenShare(units.Total)
	ideal, hasIdeal := pace.IdealHourly(share, set.Schedule.HoursPerDay)
	gauge(snap, ideal, hasIdeal)
}

func gauge(snap *WindowSnapshot, ideal float64, hasIdeal bool) {
	snap.Target, snap.HasTarget = pace.Target(snap.Ceiling, snap.HasCeiling, ideal, hasIdeal)
	if snap.HasTarget {
		v := 0.0
		if snap.HasInstantPace {
			v = snap.InstantPace
		}
		snap.Gauge, snap.HasGauge = pace.GaugePosition(v, snap.Target), true
	}
}

func tip(snap WindowSnapshot, draw float64) *advice.Tip {
	in := advice.TipInput{Used: snap.Used, Rating: snap.Rating, DaysToReset: snap.DaysToReset, HasDaysToReset: snap.HasDaysToReset}
	if s := snap.Summary; s != nil {
		in.Preliminary, in.Autonomy, in.HasAutonomy, in.WorkingUnitsToReset = s.Preliminary, s.Autonomy, s.HasAutonomy, s.WorkingUnitsToReset
	}
	if c := snap.Cause; c != nil {
		in.Cause = &advice.Cause{HighPercent: float64(c.HighDaysHighContextPercent), LowPercent: float64(c.LowDaysHighContextPercent), Days: c.DaysCompared}
	}
	picked, ok := advice.PickTip(advice.TipCandidates(in), draw)
	if !ok {
		return nil
	}
	return &picked
}

func perUnit(r *outlook.Redistribution) float64 {
	if r == nil {
		return 0
	}
	return r.PerUnit
}

// record applies the read to the window's history through history.Record,
// as the refresh loop does (internal/refresh): the read's own moment decides
// its day, its period and the previous day, not the moment the snapshot is
// built (a read just before midnight belongs to the day and the period it was
// taken in). A stale read is ignored by history.Record (D4).
func record(in Input, w Window, x float64) history.State {
	stored, at := in.History[w.ID], in.Response.FetchedAt
	if !in.Response.Fresh {
		return stored
	}
	period, err := quotaperiod.ResolvePeriod(
		quotaperiod.Window{Kind: w.Kind, Length: w.Length, ResetsAt: w.ResetsAt},
		in.Response.SubscriptionRenewsAt, in.Settings.Renewal, at, in.Settings.Location)
	if err != nil {
		period = quotaperiod.Period{Kind: w.Kind}
	}
	return history.Record(stored, history.Sample{At: at, Used: x, Fresh: true}, at, recordSettings(stored, period, in.Settings, at))
}

// recordSettings gives history.Record the calendar, the retention and the
// window's period at the read's moment: the period start and the last point of
// an earlier local day, whose time is not stored (the end of its day stands
// for it).
func recordSettings(state history.State, period quotaperiod.Period, set Settings, at time.Time) history.Settings {
	retention := set.RetentionDays
	if retention <= 0 {
		retention = defaultRetentionDays
	}
	out := history.Settings{Location: set.Location, RetentionDays: retention, Period: history.PeriodInput{Start: period.Start}}
	today := startOfDay(at, set.Location)
	var previous *history.DailyPoint
	for i, point := range state.DailyUsage {
		if point.Day.Before(today) && (previous == nil || point.Day.After(previous.Day)) {
			previous = &state.DailyUsage[i]
		}
	}
	if previous != nil {
		out.Period.PreviousAt = previous.Day.AddDate(0, 0, 1).Add(-time.Nanosecond)
		out.Period.PreviousUsed = previous.Used
	}
	return out
}

// verdictRank orders the verdicts the widget opens on first.
var verdictRank = map[outlook.Verdict]int{outlook.Exhausted: 2, outlook.AtRisk: 1}

// Critical returns the index of the window the widget opens on: an exhausted
// or at-risk window first, then the most used, then one with pacing (§3.10).
// A window without a utilization ranks below every window with one (E14).
// It is -1 without windows.
func Critical(windows []WindowSnapshot) int {
	if len(windows) == 0 {
		return -1
	}
	order := make([]int, len(windows))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		wa, wb := windows[a], windows[b]
		return cmp.Or(
			cmp.Compare(verdictRank[wb.Judgement.Verdict], verdictRank[wa.Judgement.Verdict]),
			compareUsed(wb, wa),
			cmp.Compare(boolRank(paced(wb)), boolRank(paced(wa))),
		)
	})
	return order[0]
}

func compareUsed(a, b WindowSnapshot) int {
	if a.HasUsed != b.HasUsed {
		return cmp.Compare(boolRank(a.HasUsed), boolRank(b.HasUsed))
	}
	return cmp.Compare(a.Used, b.Used)
}

func paced(w WindowSnapshot) bool {
	return w.Units != nil || w.Hourly != nil
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

func startOfDay(at time.Time, loc *time.Location) time.Time {
	y, m, d := at.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}
