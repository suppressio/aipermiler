// Package pacingparity runs the Go pacing packages on the scenarios of the
// parity comparison with budget.ts (#325): one JSON scenario in, the
// normalized readings of docs/PACING.md out, in the shape scripts/pacing-parity.js
// also produces from the TypeScript path. Comparison and difference handling
// live in that script; this package only computes the Go side.
package pacingparity

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/pace"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/snapshot"
)

// Scenario is one account read at one moment, with the stored history of each
// window. The same file is read by the TypeScript side.
type Scenario struct {
	Name string    `json:"name"`
	Now  time.Time `json:"now"`
	// TimeZone is an IANA name: the local calendar of the day keys.
	TimeZone string `json:"timeZone"`
	// Fresh is a value just read; ReadAt is when (default Now).
	Fresh  bool       `json:"fresh"`
	ReadAt *time.Time `json:"readAt"`
	// ChartDays is 7 or 30 (default 7).
	ChartDays int          `json:"chartDays"`
	Schedule  ScheduleSpec `json:"schedule"`
	// RenewalDay is the manual renewal day of the month.
	RenewalDay           int                  `json:"renewalDay"`
	SubscriptionRenewsAt *time.Time           `json:"subscriptionRenewsAt"`
	FirstSession         *time.Time           `json:"firstSession"`
	Windows              []WindowIn           `json:"windows"`
	History              map[string]HistoryIn `json:"history"`
	// ExpectedDifferences are the differences between the two paths known and
	// explained by docs/PACING.md; read by the comparison script only.
	ExpectedDifferences []ExpectedDifference `json:"expectedDifferences"`
}

// ExpectedDifference maps field paths (windows[0].instantRate; a path covers
// what is under it) to the section 9 item or section 8 row of docs/PACING.md
// that explains them, or to "porting-error: <what>" for a difference no
// section explains, which is tracked by an issue of its own.
type ExpectedDifference struct {
	Paths []string `json:"paths"`
	Ref   string   `json:"ref"`
}

// ScheduleSpec is the work schedule as the settings store it.
type ScheduleSpec struct {
	Enabled     bool               `json:"enabled"`
	Days        map[string]float64 `json:"days"`
	HoursPerDay float64            `json:"hoursPerDay"`
}

// WindowIn is a quota window of the provider response (types/index.ts).
type WindowIn struct {
	ID           string     `json:"id"`
	PeriodType   string     `json:"periodType"`
	PeriodLength int        `json:"periodLength"`
	Unit         string     `json:"unit"`
	Used         float64    `json:"used"`
	Total        float64    `json:"total"`
	ResetsAt     *time.Time `json:"resetsAt"`
}

// HistoryIn is the stored history of one window.
type HistoryIn struct {
	Daily   []DailyIn  `json:"daily"`
	Samples []SampleIn `json:"samples"`
}

// DailyIn is a daily point keyed by its local date.
type DailyIn struct {
	Date         string   `json:"date"`
	Used         float64  `json:"used"`
	DayStartUsed *float64 `json:"dayStartUsed"`
}

// SampleIn is a timestamped reading.
type SampleIn struct {
	At   time.Time `json:"at"`
	Used float64   `json:"used"`
}

// Output is the normalized result of a scenario.
type Output struct {
	Name     string      `json:"name"`
	Critical int         `json:"critical"`
	Windows  []WindowOut `json:"windows"`
}

// WindowOut holds the readings both paths produce. A nil pointer is null.
type WindowOut struct {
	ID                    string          `json:"id"`
	EfficiencyIndex       *float64        `json:"efficiencyIndex"`
	ProjectedUsage        *float64        `json:"projectedUsage"`
	DaysUntilReset        *float64        `json:"daysUntilReset"`
	WorkingDaysUntilReset *float64        `json:"workingDaysUntilReset"`
	AutonomyWorkingDays   *float64        `json:"autonomyWorkingDays"`
	TodayBudget           *TodayBudgetOut `json:"todayBudget"`
	Redistribution        *RedistOut      `json:"redistribution"`
	Hourly                *HourlyOut      `json:"hourly"`
	Preliminary           bool            `json:"preliminary"`
	InstantRate           *float64        `json:"instantRate"`
	SustainableRate       *float64        `json:"sustainableRate"`
	Rating                *RatingOut      `json:"rating"`
	Verdict               string          `json:"verdict"`
	Chart                 []SlotOut       `json:"chart"`
	Stats                 StatsOut        `json:"stats"`
	History               HistoryOut      `json:"history"`
}

// TodayBudgetOut is today's budget and consumption.
type TodayBudgetOut struct {
	Budget    float64 `json:"budget"`
	UsedToday float64 `json:"usedToday"`
}

// RedistOut is the remaining quota per working day next to the even share.
type RedistOut struct {
	PerUnit      float64 `json:"perUnit"`
	IdealPerUnit float64 `json:"idealPerUnit"`
	UnitsLeft    float64 `json:"unitsLeft"`
}

// HourlyOut is the reading of a rolling-hours window.
type HourlyOut struct {
	HoursLeft        float64  `json:"hoursLeft"`
	ProjectedAtReset float64  `json:"projectedAtReset"`
	AutonomyHours    *float64 `json:"autonomyHours"`
}

// RatingOut is the star rating.
type RatingOut struct {
	Stars    int     `json:"stars"`
	AvgRatio float64 `json:"avgRatio"`
}

// SlotOut is one slot of the chart.
type SlotOut struct {
	Date       string   `json:"date"`
	Delta      *float64 `json:"delta"`
	FullShare  *float64 `json:"fullShare"`
	DayUnit    float64  `json:"dayUnit"`
	Budget     *float64 `json:"budget"`
	Upcoming   bool     `json:"upcoming"`
	OverBudget bool     `json:"overBudget"`
}

// StatsOut is peak, average and streak of the chart.
type StatsOut struct {
	Peak   *float64 `json:"peak"`
	Avg    *float64 `json:"avg"`
	Streak *int     `json:"streak"`
}

// HistoryOut is the window's history after the read.
type HistoryOut struct {
	Daily   []DailyOut  `json:"daily"`
	Samples []SampleOut `json:"samples"`
}

// DailyOut is a daily point.
type DailyOut struct {
	Date         string   `json:"date"`
	Used         float64  `json:"used"`
	DayStartUsed *float64 `json:"dayStartUsed"`
}

// SampleOut is a reading.
type SampleOut struct {
	At   string  `json:"at"`
	Used float64 `json:"used"`
}

// Parse reads one scenario.
func Parse(data []byte) (Scenario, error) {
	var s Scenario
	if err := json.Unmarshal(data, &s); err != nil {
		return Scenario{}, err
	}
	if s.Name == "" || s.Now.IsZero() || s.TimeZone == "" {
		return Scenario{}, fmt.Errorf("scenario needs name, now and timeZone")
	}
	// What follows keeps the two sides reading the scenario the same way
	// (scripts/pacing-parity.js runs the Go side first, so these checks hold
	// for both).
	if s.RenewalDay == 0 {
		s.RenewalDay = 1 // as the TypeScript side
	}
	if s.RenewalDay < 1 || s.RenewalDay > 31 {
		return Scenario{}, fmt.Errorf("%s: renewalDay %d is not 1-31", s.Name, s.RenewalDay)
	}
	for day, weight := range s.Schedule.Days {
		if !slices.Contains(weekdays[:], day) {
			return Scenario{}, fmt.Errorf("%s: unknown day %q (mon..sun)", s.Name, day)
		}
		if weight != 0 && weight != 0.5 && weight != 1 {
			return Scenario{}, fmt.Errorf("%s: %s weighs %v: TypeScript knows only 1, 0.5 and 0", s.Name, day, weight)
		}
	}
	if !(s.Schedule.HoursPerDay > 0) {
		return Scenario{}, fmt.Errorf("%s: schedule.hoursPerDay is missing or not positive", s.Name)
	}
	if s.ChartDays == 0 {
		s.ChartDays = 7 // as the TypeScript side
	}
	if s.ChartDays != 7 && s.ChartDays != 30 {
		return Scenario{}, fmt.Errorf("%s: chartDays %d is not 7 or 30", s.Name, s.ChartDays)
	}
	if s.ReadAt != nil && s.ReadAt.After(s.Now) {
		return Scenario{}, fmt.Errorf("%s: readAt is after now", s.Name)
	}
	return s, nil
}

// Run builds the Go snapshot of a scenario.
func Run(s Scenario) (Output, error) {
	loc, err := time.LoadLocation(s.TimeZone)
	if err != nil {
		return Output{}, err
	}
	in, err := input(s, loc)
	if err != nil {
		return Output{}, err
	}
	res := snapshot.Build(in)
	out := Output{Name: s.Name, Critical: res.Account.Critical, Windows: make([]WindowOut, 0, len(res.Account.Windows))}
	for _, w := range res.Account.Windows {
		out.Windows = append(out.Windows, windowOut(w, res.History[w.ID], loc))
	}
	return out, nil
}

var weekdays = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

func input(s Scenario, loc *time.Location) (snapshot.Input, error) {
	var days [7]float64
	for i, name := range weekdays {
		days[i] = s.Schedule.Days[name]
	}
	chartDays := s.ChartDays
	if chartDays == 0 {
		chartDays = 7
	}
	set := snapshot.Settings{
		Location:  loc,
		Schedule:  quotaperiod.Schedule{Enabled: s.Schedule.Enabled, Days: days, HoursPerDay: s.Schedule.HoursPerDay},
		Renewal:   quotaperiod.RenewalRule{Type: "dayOfMonth", Day: s.RenewalDay},
		ChartDays: chartDays,
	}
	if s.FirstSession != nil {
		set.FirstSession = *s.FirstSession
	}
	resp := snapshot.Response{FetchedAt: s.Now, Fresh: s.Fresh}
	if s.ReadAt != nil && s.Fresh {
		resp.FetchedAt = *s.ReadAt
	}
	if s.SubscriptionRenewsAt != nil {
		resp.SubscriptionRenewsAt = *s.SubscriptionRenewsAt
	}
	for _, w := range s.Windows {
		win := snapshot.Window{ID: w.ID, Kind: quotaperiod.Kind(w.PeriodType), Length: w.PeriodLength, Unit: pace.Unit(w.Unit), Used: w.Used, Total: w.Total}
		if w.ResetsAt != nil {
			win.ResetsAt = *w.ResetsAt
		}
		resp.Windows = append(resp.Windows, win)
	}
	hist := map[string]history.State{}
	for id, h := range s.History {
		st := history.State{}
		for _, d := range h.Daily {
			day, err := time.ParseInLocation(time.DateOnly, d.Date, loc)
			if err != nil {
				return snapshot.Input{}, fmt.Errorf("history %s: %w", id, err)
			}
			start := d.Used
			if d.DayStartUsed != nil {
				start = *d.DayStartUsed
			}
			st.DailyUsage = append(st.DailyUsage, history.DailyPoint{Day: day, Used: d.Used, DayStartUsed: start})
		}
		for _, r := range h.Samples {
			st.RecentSamples = append(st.RecentSamples, history.Reading{At: r.At, Used: r.Used})
		}
		hist[id] = st
	}
	return snapshot.Input{History: hist, Response: resp, Settings: set, Now: s.Now}, nil
}

func windowOut(w snapshot.WindowSnapshot, h history.State, loc *time.Location) WindowOut {
	out := WindowOut{ID: w.ID, Verdict: string(w.Judgement.Verdict), Chart: []SlotOut{}, History: historyOut(h, loc)}
	if !w.HasUsed {
		return out
	}
	if w.HasEfficiency {
		out.EfficiencyIndex = ptr(w.Efficiency)
	}
	// The widget shows no day metric on a rolling-hours window (it reads it in
	// hours, §3.9) and the TypeScript path reports none there.
	if w.HasDaysToReset && w.Hourly == nil {
		out.DaysUntilReset = ptr(w.DaysToReset)
	}
	if w.HasInstantPace {
		out.InstantRate = ptr(w.InstantPace)
	}
	if w.HasCeiling {
		out.SustainableRate = ptr(w.Ceiling)
	}
	if s := w.Summary; s != nil {
		out.ProjectedUsage = ptr(s.Projection)
		out.WorkingDaysUntilReset = ptr(s.WorkingUnitsToReset)
		if s.HasAutonomy {
			out.AutonomyWorkingDays = ptr(s.Autonomy)
		}
		out.Preliminary = s.Preliminary
	}
	if w.HasTodayBudget {
		out.TodayBudget = &TodayBudgetOut{Budget: w.TodayBudget, UsedToday: w.UsedToday}
	}
	if r := w.Redistribution; r != nil {
		out.Redistribution = &RedistOut{PerUnit: r.PerUnit, IdealPerUnit: r.EvenShare, UnitsLeft: r.Units}
	}
	if h := w.Hourly; h != nil {
		out.Hourly = &HourlyOut{HoursLeft: h.HoursLeft, ProjectedAtReset: h.ProjectedAtReset}
		out.ProjectedUsage = ptr(h.ProjectedAtReset)
		if h.HasAutonomy {
			out.Hourly.AutonomyHours = ptr(h.AutonomyHours)
		}
	}
	if r := w.Rating; r != nil {
		out.Rating = &RatingOut{Stars: r.Stars, AvgRatio: r.Average}
	}
	for _, s := range w.Chart {
		slot := SlotOut{Date: s.Day.In(loc).Format(time.DateOnly), DayUnit: s.DayUnit, Upcoming: s.Upcoming, OverBudget: s.OverBudget}
		if s.HasDelta {
			slot.Delta = ptr(s.Delta)
		}
		if s.HasFullShare {
			slot.FullShare = ptr(s.FullShare)
		}
		if s.HasBudget {
			slot.Budget = ptr(s.Budget)
		}
		out.Chart = append(out.Chart, slot)
	}
	if w.Stats.HasPeakAverage {
		out.Stats.Peak, out.Stats.Avg = ptr(w.Stats.Peak), ptr(w.Stats.Average)
	}
	if w.Stats.HasStreak {
		out.Stats.Streak = ptr(w.Stats.Streak)
	}
	return out
}

func historyOut(h history.State, loc *time.Location) HistoryOut {
	out := HistoryOut{Daily: []DailyOut{}, Samples: []SampleOut{}}
	for _, d := range h.DailyUsage {
		out.Daily = append(out.Daily, DailyOut{Date: d.Day.In(loc).Format(time.DateOnly), Used: d.Used, DayStartUsed: ptr(d.DayStartUsed)})
	}
	slices.SortFunc(out.Daily, func(a, b DailyOut) int { return compareStrings(a.Date, b.Date) })
	for _, r := range h.RecentSamples {
		out.Samples = append(out.Samples, SampleOut{At: r.At.UTC().Format(time.RFC3339), Used: r.Used})
	}
	return out
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func ptr[T any](v T) *T { return &v }
