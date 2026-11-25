package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/advice"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/outlook"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/pace"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/snapshot"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// snapshotAPI serves the pacing snapshot (#326): the account's usage document
// with one more field, pacing, built by the snapshot package from the store's
// history and settings, the account's lastGood and the request's moment.
type snapshotAPI struct {
	accounts accountsAPI
	now      func() time.Time
	location *time.Location
	tipDraw  func() float64
}

func newSnapshotAPI(accounts accountsAPI, cfg HandlerConfig) snapshotAPI {
	s := snapshotAPI{accounts: accounts, now: cfg.Now, location: cfg.Location, tipDraw: cfg.TipDraw}
	if s.now == nil {
		s.now = time.Now
	}
	if s.location == nil {
		s.location = time.Local
	}
	if s.tipDraw == nil {
		s.tipDraw = randomDraw
	}
	return s
}

func (s snapshotAPI) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/accounts/{id}/snapshot", s.snapshot)
}

func (s snapshotAPI) snapshot(w http.ResponseWriter, r *http.Request) {
	chartDays := 7
	if q := r.URL.Query(); q.Has("chartDays") {
		switch q.Get("chartDays") {
		case "7":
		case "30":
			chartDays = 30
		default:
			writeUsageError(w, http.StatusBadRequest, "chartDays must be 7 or 30")
			return
		}
	}
	account, ok := s.accounts.enabledAccount(w, r)
	if !ok {
		return
	}
	// Freshness, errors and their status are exactly the usage endpoint's:
	// without a good read there is nothing to pace.
	if account.LastGood == nil {
		doc, status := s.accounts.document(account)
		writeDocument(w, status, doc)
		return
	}
	lg := *account.LastGood
	u := lastGoodUsage(lg, s.accounts.loop.LastError(account.ID))
	envelope := usage.SuccessDocument{
		SchemaVersion: usage.SchemaVersion, Provider: account.Provider,
		FetchedAt: lg.UpdatedAt.UTC().Format(time.RFC3339),
		Stale:     u.Stale != nil, StaleReason: u.Stale, Usage: u,
	}
	settings := s.accounts.store.Snapshot().Settings
	result := snapshot.Build(snapshot.Input{
		History:  account.History,
		Response: response(u, lg.UpdatedAt),
		Settings: snapshot.Settings{
			Location:      s.location,
			Schedule:      quotaperiod.Schedule(account.Schedule),
			Renewal:       quotaperiod.RenewalRule{Type: "dayOfMonth", Day: account.RenewalDay},
			RetentionDays: settings.RetentionDays,
			ChartDays:     chartDays,
			TipDraw:       s.tipDraw(),
		},
		Now: s.now(),
	})
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(struct {
		usage.SuccessDocument
		Pacing pacingDoc `json:"pacing"`
	}{envelope, newPacingDoc(result.Account, s.location)})
	if err != nil { // a non-finite value: never expected, never served as a number
		writeDocument(w, http.StatusInternalServerError, errorDocument(account.Provider, errors.New("pacing snapshot: "+err.Error())))
		return
	}
	writeDocument(w, http.StatusOK, buf.Bytes())
}

// response is the lastGood (u, read at fetchedAt) as a provider read already
// recorded: Fresh is false, so the snapshot adds no sample (the loop recorded
// it, I-12) and the endpoint never writes the store; the returned history is
// dropped.
func response(u usage.Usage, fetchedAt time.Time) snapshot.Response {
	out := snapshot.Response{FetchedAt: fetchedAt, Windows: make([]snapshot.Window, 0, len(u.QuotaWindows))}
	if u.SubscriptionRenewsAt != nil {
		out.SubscriptionRenewsAt = *u.SubscriptionRenewsAt
	}
	for _, q := range u.QuotaWindows {
		w := snapshot.Window{ID: q.ID, Kind: quotaperiod.Kind(q.PeriodType), Unit: pace.Unit(q.Unit), Used: q.Used}
		if q.PeriodLength != nil {
			w.Length = int(*q.PeriodLength)
		}
		if q.Total != nil {
			w.Total = *q.Total
		}
		if q.ResetsAt != nil {
			w.ResetsAt = *q.ResetsAt
		}
		out.Windows = append(out.Windows, w)
	}
	return out
}

// The JSON of the pacing snapshot. Values are exact (I-4); an undefined value
// is null, never 0 (I-5).
type pacingDoc struct {
	Critical int         `json:"critical"`
	Windows  []windowDoc `json:"windows"`
}

type windowDoc struct {
	ID             string             `json:"id"`
	Used           *float64           `json:"used"`
	Remaining      *float64           `json:"remaining"`
	Period         *periodDoc         `json:"period"`
	Units          *unitsDoc          `json:"units"`
	DaysToReset    *float64           `json:"daysToReset"`
	Efficiency     *float64           `json:"efficiency"`
	Baseline       *float64           `json:"baseline"`
	UsedToday      *float64           `json:"usedToday"`
	TodayBudget    *float64           `json:"todayBudget"`
	PaceAlert      bool               `json:"paceAlert"`
	Redistribution *redistributionDoc `json:"redistribution"`
	Summary        *summaryDoc        `json:"summary"`
	Hourly         *hourlyDoc         `json:"hourly"`
	InstantPace    *float64           `json:"instantPace"`
	Ceiling        *float64           `json:"ceiling"`
	Target         *float64           `json:"target"`
	Gauge          *float64           `json:"gauge"`
	Judgement      judgementDoc       `json:"judgement"`
	Chart          []slotDoc          `json:"chart"`
	Stats          statsDoc           `json:"stats"`
	Rating         *ratingDoc         `json:"rating"`
	Tip            *tipDoc            `json:"tip"`
}

// periodDoc has a null bound where it is unknown (a billing cycle of unknown
// length knows its end only).
type periodDoc struct {
	Start *time.Time `json:"start"`
	End   *time.Time `json:"end"`
}

type unitsDoc struct {
	Total          float64 `json:"total"`
	Elapsed        float64 `json:"elapsed"`
	Remaining      float64 `json:"remaining"`
	FromToday      float64 `json:"fromToday"`
	TodayElapsed   float64 `json:"todayElapsed"`
	RemainingHours float64 `json:"remainingHours"`
}

type redistributionDoc struct {
	PerUnit   float64 `json:"perUnit"`
	EvenShare float64 `json:"evenShare"`
	Units     float64 `json:"units"`
}

type summaryDoc struct {
	Pace                *float64 `json:"pace"`
	Projection          float64  `json:"projection"`
	Autonomy            *float64 `json:"autonomy"`
	WorkingUnitsToReset float64  `json:"workingUnitsToReset"`
	Preliminary         bool     `json:"preliminary"`
}

type hourlyDoc struct {
	HoursLeft        float64  `json:"hoursLeft"`
	ProjectedAtReset float64  `json:"projectedAtReset"`
	AutonomyHours    *float64 `json:"autonomyHours"`
	ResetPassed      bool     `json:"resetPassed"`
}

type judgementDoc struct {
	Verdict       outlook.Verdict `json:"verdict"`
	Reason        *outlook.Reason `json:"reason"`
	AutonomyHours *float64        `json:"autonomyHours"`
}

type slotDoc struct {
	// Day is the local day, YYYY-MM-DD.
	Day        string   `json:"day"`
	Delta      *float64 `json:"delta"`
	FullShare  *float64 `json:"fullShare"`
	DayUnit    float64  `json:"dayUnit"`
	Budget     *float64 `json:"budget"`
	Upcoming   bool     `json:"upcoming"`
	OverBudget bool     `json:"overBudget"`
}

type statsDoc struct {
	Days    int      `json:"days"`
	Peak    *float64 `json:"peak"`
	Average *float64 `json:"average"`
	Streak  *int     `json:"streak"`
}

type ratingDoc struct {
	Stars   int     `json:"stars"`
	Average float64 `json:"average"`
}

type tipDoc struct {
	Key    advice.TipKey      `json:"key"`
	Params map[string]float64 `json:"params"`
}

func newPacingDoc(a snapshot.Account, loc *time.Location) pacingDoc {
	out := pacingDoc{Critical: a.Critical, Windows: make([]windowDoc, 0, len(a.Windows))}
	for _, w := range a.Windows {
		out.Windows = append(out.Windows, newWindowDoc(w, loc))
	}
	return out
}

func newWindowDoc(w snapshot.WindowSnapshot, loc *time.Location) windowDoc {
	d := windowDoc{
		ID:          w.ID,
		Used:        opt(w.Used, w.HasUsed),
		Remaining:   opt(w.Remaining, w.HasUsed),
		DaysToReset: opt(w.DaysToReset, w.HasDaysToReset),
		Efficiency:  opt(w.Efficiency, w.HasEfficiency),
		Baseline:    opt(w.Baseline, w.HasBaseline),
		UsedToday:   opt(w.UsedToday, w.HasUsedToday),
		TodayBudget: opt(w.TodayBudget, w.HasTodayBudget),
		PaceAlert:   w.PaceAlert,
		InstantPace: opt(w.InstantPace, w.HasInstantPace),
		Ceiling:     opt(w.Ceiling, w.HasCeiling),
		Target:      opt(w.Target, w.HasTarget),
		Gauge:       opt(w.Gauge, w.HasGauge),
		Judgement: judgementDoc{
			Verdict:       w.Judgement.Verdict,
			Reason:        opt(w.Judgement.Reason, w.Judgement.Reason != outlook.ReasonNone),
			AutonomyHours: opt(w.Judgement.AutonomyHours, w.Judgement.HasAutonomy),
		},
		Chart: make([]slotDoc, 0, len(w.Chart)),
		Stats: statsDoc{
			Days:    w.Stats.Days,
			Peak:    opt(w.Stats.Peak, w.Stats.HasPeakAverage),
			Average: opt(w.Stats.Average, w.Stats.HasPeakAverage),
			Streak:  opt(w.Stats.Streak, w.Stats.HasStreak),
		},
	}
	if start, end := w.Period.Start, w.Period.End; !start.IsZero() || !end.IsZero() {
		d.Period = &periodDoc{Start: opt(start.UTC(), !start.IsZero()), End: opt(end.UTC(), !end.IsZero())}
	}
	if u := w.Units; u != nil {
		d.Units = &unitsDoc{u.Total, u.Elapsed, u.Remaining, u.FromToday, u.TodayElapsed, u.RemainingHours}
	}
	if r := w.Redistribution; r != nil {
		d.Redistribution = &redistributionDoc{r.PerUnit, r.EvenShare, r.Units}
	}
	if s := w.Summary; s != nil {
		d.Summary = &summaryDoc{opt(s.Pace, s.HasPace), s.Projection, opt(s.Autonomy, s.HasAutonomy), s.WorkingUnitsToReset, s.Preliminary}
	}
	if h := w.Hourly; h != nil {
		d.Hourly = &hourlyDoc{h.HoursLeft, h.ProjectedAtReset, opt(h.AutonomyHours, h.HasAutonomy), h.ResetPassed}
	}
	for _, slot := range w.Chart {
		d.Chart = append(d.Chart, slotDoc{
			Day:   slot.Day.In(loc).Format(time.DateOnly),
			Delta: opt(slot.Delta, slot.HasDelta), FullShare: opt(slot.FullShare, slot.HasFullShare),
			DayUnit: slot.DayUnit, Budget: opt(slot.Budget, slot.HasBudget),
			Upcoming: slot.Upcoming, OverBudget: slot.OverBudget,
		})
	}
	if r := w.Rating; r != nil {
		d.Rating = &ratingDoc{r.Stars, r.Average}
	}
	if t := w.Tip; t != nil {
		d.Tip = &tipDoc{t.Key, t.Params}
	}
	return d
}

// opt is v when it is defined, nil otherwise.
func opt[T any](v T, defined bool) *T {
	if !defined {
		return nil
	}
	return &v
}

// randomDraw picks a tip as Electron does today (Math.random per snapshot):
// variety, not security.
func randomDraw() float64 { return rand.Float64() } //nolint:gosec // a tip choice, not a secret
