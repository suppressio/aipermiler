package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// defaultHistoryDays is the diagnostic report's span: the daily points from
// 7 days before today through today.
const defaultHistoryDays = 7

// historyAPI serves an account's history read-only (#339), for the
// diagnostic report: the daily points of the last days and today's samples,
// as the store keeps them.
type historyAPI struct {
	accounts accountsAPI
	now      func() time.Time
	location *time.Location
}

func (h historyAPI) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/accounts/{id}/history", h.history)
}

type historyDailyDoc struct {
	Day          string  `json:"day"`
	Used         float64 `json:"used"`
	DayStartUsed float64 `json:"dayStartUsed"`
}

type historySampleDoc struct {
	At   string  `json:"at"`
	Used float64 `json:"used"`
}

type historyWindowDoc struct {
	ID           string             `json:"id"`
	Daily        []historyDailyDoc  `json:"daily"`
	SamplesToday []historySampleDoc `json:"samplesToday"`
}

func (h historyAPI) history(w http.ResponseWriter, r *http.Request) {
	account, ok := h.accounts.enabledAccount(w, r)
	if !ok {
		return
	}
	days, ok := historyDaysOf(r)
	if !ok {
		writeUsageError(w, http.StatusBadRequest, "days must be given once, an integer 0-"+strconv.Itoa(maxRetentionDays))
		return
	}
	now := h.now().In(h.location)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, h.location)
	cutoff := today.AddDate(0, 0, -days)
	ids := make([]string, 0, len(account.History))
	for id := range account.History {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	windows := make([]historyWindowDoc, 0, len(ids))
	for _, id := range ids {
		windows = append(windows, historyWindow(id, account.History[id], cutoff, today, h.location))
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	// Only strings, ints and float64 values the store already holds as finite
	// numbers: encoding cannot fail.
	_ = encoder.Encode(struct {
		SchemaVersion int    `json:"schemaVersion"`
		Provider      string `json:"provider"`
		History       struct {
			Days    int                `json:"days"`
			Windows []historyWindowDoc `json:"windows"`
		} `json:"history"`
	}{
		SchemaVersion: usage.SchemaVersion, Provider: account.Provider,
		History: struct {
			Days    int                `json:"days"`
			Windows []historyWindowDoc `json:"windows"`
		}{days, windows},
	})
	writeDocument(w, http.StatusOK, buf.Bytes())
}

// historyWindow is one window's daily points from cutoff on and its samples
// of the local day starting at today.
func historyWindow(id string, state history.State, cutoff, today time.Time, loc *time.Location) historyWindowDoc {
	doc := historyWindowDoc{ID: id, Daily: []historyDailyDoc{}, SamplesToday: []historySampleDoc{}}
	for _, p := range state.DailyUsage {
		if !p.Day.Before(cutoff) {
			doc.Daily = append(doc.Daily, historyDailyDoc{Day: p.Day.In(loc).Format(time.DateOnly), Used: p.Used, DayStartUsed: p.DayStartUsed})
		}
	}
	tomorrow := today.AddDate(0, 0, 1)
	for _, s := range state.RecentSamples {
		if !s.At.Before(today) && s.At.Before(tomorrow) {
			doc.SamplesToday = append(doc.SamplesToday, historySampleDoc{At: s.At.UTC().Format(time.RFC3339), Used: s.Used})
		}
	}
	return doc
}

// historyDaysOf reads the optional days parameter: an integer 0 to the
// longest retention (defaultHistoryDays when absent), given at most once;
// false for anything else.
func historyDaysOf(r *http.Request) (int, bool) {
	values, given := r.URL.Query()["days"]
	if !given {
		return defaultHistoryDays, true
	}
	if len(values) != 1 {
		return 0, false
	}
	v := values[0]
	if v == "" || v[0] < '0' || v[0] > '9' { // Atoi accepts a sign
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n > maxRetentionDays {
		return 0, false
	}
	return n, true
}
