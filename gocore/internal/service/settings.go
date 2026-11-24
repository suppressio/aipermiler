package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"

	"github.com/suppressio/aipermiler/gocore/internal/github"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/secrets"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const (
	// maxSettingsBody bounds PUT /api/v1/settings: a few accounts are a few KiB.
	maxSettingsBody = 256 << 10
	// reasonInvalidSettings tells a rejected settings document from the other
	// usage errors.
	reasonInvalidSettings = "invalid_settings"
	minRefreshMinutes     = 5 // policy.FastRefresh: a shorter interval reads no sooner
	maxRefreshMinutes     = 24 * 60
	maxRetentionDays      = 3650
)

// settingsDocument is GET and PUT /api/v1/settings: the store's settings and
// every account's settings, never its history, last read or any secret (the
// Copilot token is in the keyring, #179).
type settingsDocument struct {
	SchemaVersion int               `json:"schemaVersion"`
	Settings      store.Settings    `json:"settings"`
	Accounts      []accountSettings `json:"accounts"`
}

// accountSettings is the editable part of a store.Account. Host, Scope and
// ManualQuota are Copilot's, BinaryPath Claude's and Codex's.
type accountSettings struct {
	ID          string           `json:"id"`
	Provider    string           `json:"provider"`
	Label       string           `json:"label"`
	Enabled     bool             `json:"enabled"`
	RenewalDay  int              `json:"renewalDay"`
	Schedule    scheduleSettings `json:"schedule"`
	Host        string           `json:"host,omitempty"`
	Scope       string           `json:"scope,omitempty"`
	ManualQuota float64          `json:"manualQuota,omitempty"`
	BinaryPath  *string          `json:"binaryPath,omitempty"`
}

// scheduleSettings is store.Schedule with a slice, so a wrong number of days
// is seen instead of padded or cut by the decoder.
type scheduleSettings struct {
	Enabled     bool      `json:"enabled"`
	Days        []float64 `json:"days"`
	HoursPerDay float64   `json:"hoursPerDay"`
}

// settingsRequest is a PUT body: pointers tell a missing field from a zero.
type settingsRequest struct {
	SchemaVersion *int               `json:"schemaVersion"`
	Settings      *store.Settings    `json:"settings"`
	Accounts      *[]accountSettings `json:"accounts"`
}

// invalidSettings is a rejected document: the field (a JSON path) and why.
type invalidSettings struct{ field, problem string }

func (e *invalidSettings) Error() string { return e.field + ": " + e.problem }

func invalid(field, format string, args ...any) *invalidSettings {
	return &invalidSettings{field, fmt.Sprintf(format, args...)}
}

// settingsAPI reads and writes the settings through the store, whose one
// writer is the service (#146), and tells the loop which accounts' reads
// changed (#284).
type settingsAPI struct {
	store *store.Store
	loop  Loop
}

func (a settingsAPI) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings", a.get)
	mux.HandleFunc("PUT /api/v1/settings", a.put)
}

func (a settingsAPI) get(w http.ResponseWriter, _ *http.Request) {
	writeSettings(w, a.store.Snapshot())
}

// put replaces the settings and the account list with the body's, all or
// nothing. Accounts keep their history and last read by id. Accounts added,
// removed, enabled, disabled or read differently go to Loop.Changed once the
// change is committed: the loop forgets them and reads them at once.
func (a settingsAPI) put(w http.ResponseWriter, r *http.Request) {
	req, err := decodeSettings(http.MaxBytesReader(w, r.Body, maxSettingsBody))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeInvalidSettings(w, status, err.Error())
		return
	}
	var changed []string
	err = a.store.Update(func(s *store.State) error {
		accounts, err := applyAccounts(s.Accounts, *req.Accounts)
		if err != nil {
			return err
		}
		changed = changedAccounts(s.Accounts, accounts)
		s.Settings = *req.Settings
		s.Accounts = accounts
		return nil
	})
	var bad *invalidSettings
	switch {
	case errors.As(err, &bad):
		writeInvalidSettings(w, http.StatusBadRequest, bad.Error())
		return
	case err != nil && !errors.Is(err, store.ErrNotSynced): // ErrNotSynced is committed
		writeUnavailable(w, "Cannot save the settings: "+err.Error())
		return
	}
	if len(changed) > 0 {
		a.loop.Changed(changed...)
	}
	writeSettings(w, a.store.Snapshot())
}

var errBodyTooLarge = &invalidSettings{"body", fmt.Sprintf("over %d KiB", maxSettingsBody>>10)}

// decodeSettings reads one JSON object and checks every field that does not
// depend on the store. Unknown fields are ignored (additive changes).
func decodeSettings(body io.Reader) (settingsRequest, error) {
	var req settingsRequest
	dec := json.NewDecoder(body)
	err := dec.Decode(&req)
	if err == nil {
		if _, tokErr := dec.Token(); !errors.Is(tokErr, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	var tooLarge *http.MaxBytesError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &tooLarge):
		return req, errBodyTooLarge
	case errors.As(err, &typeErr) && typeErr.Field != "":
		return req, invalid(typeErr.Field, "must be a JSON %s", typeErr.Type)
	case err != nil:
		return req, invalid("body", "must be one JSON settings document")
	}
	if req.SchemaVersion == nil || *req.SchemaVersion != usage.SchemaVersion {
		return req, invalid("schemaVersion", "must be %d", usage.SchemaVersion)
	}
	if req.Settings == nil {
		return req, invalid("settings", "required")
	}
	if req.Accounts == nil {
		return req, invalid("accounts", "required")
	}
	if err := checkSettings(*req.Settings); err != nil {
		return req, err
	}
	seen := map[string]bool{}
	for i := range *req.Accounts {
		account := &(*req.Accounts)[i]
		if err := checkAccount(fmt.Sprintf("accounts[%d]", i), account); err != nil {
			return req, err
		}
		if seen[account.ID] {
			return req, invalid(fmt.Sprintf("accounts[%d].id", i), "%q appears twice", account.ID)
		}
		seen[account.ID] = true
	}
	return req, nil
}

func checkSettings(s store.Settings) error {
	switch {
	case s.RefreshIntervalMinutes < minRefreshMinutes || s.RefreshIntervalMinutes > maxRefreshMinutes:
		return invalid("settings.refreshIntervalMinutes", "must be %d-%d", minRefreshMinutes, maxRefreshMinutes)
	case s.RetentionDays < 1 || s.RetentionDays > maxRetentionDays:
		return invalid("settings.retentionDays", "must be 1-%d", maxRetentionDays)
	case s.NotificationThresholdPercent < 1 || s.NotificationThresholdPercent > 100:
		return invalid("settings.notificationThresholdPercent", "must be 1-100")
	}
	return nil
}

// checkAccount validates one account and normalizes it in place: the host as
// github.NormalizeHost gives it, an empty binary path as none.
func checkAccount(path string, a *accountSettings) error {
	field := func(name string) string { return path + "." + name }
	if !secrets.ValidAccount(a.ID) {
		return invalid(field("id"), "must be 1-128 letters, digits, '.', '_' or '-'")
	}
	copilot := a.Provider == "copilot"
	switch a.Provider {
	case "claude", "codex", "copilot":
	default:
		return invalid(field("provider"), "must be claude, codex or copilot")
	}
	if a.RenewalDay < 0 || a.RenewalDay > 31 {
		return invalid(field("renewalDay"), "must be 0 (none) or 1-31")
	}
	if len(a.Schedule.Days) != 7 {
		return invalid(field("schedule.days"), "must have 7 weights, Sunday first")
	}
	for _, d := range a.Schedule.Days {
		if d != 0 && d != 0.5 && d != 1 {
			return invalid(field("schedule.days"), "weights must be 0, 0.5 or 1")
		}
	}
	if a.Schedule.HoursPerDay <= 0 || a.Schedule.HoursPerDay > 24 {
		return invalid(field("schedule.hoursPerDay"), "must be over 0 and at most 24")
	}
	if a.Host != "" {
		host := github.NormalizeHost(a.Host)
		if !copilot || host == "" {
			return invalid(field("host"), "must be github.com or <tenant>.ghe.com, on copilot accounts only")
		}
		a.Host = host
	}
	if a.Scope != "" && (!copilot || (a.Scope != "personal" && a.Scope != "organization")) {
		return invalid(field("scope"), "must be personal or organization, on copilot accounts only")
	}
	if a.ManualQuota != 0 && (!copilot || a.ManualQuota < 0) {
		return invalid(field("manualQuota"), "must be 0 or more, on copilot accounts only")
	}
	if a.BinaryPath != nil && *a.BinaryPath == "" {
		a.BinaryPath = nil
	}
	if a.BinaryPath != nil && (copilot || !filepath.IsAbs(*a.BinaryPath)) {
		return invalid(field("binaryPath"), "must be an absolute path, on claude and codex accounts only")
	}
	return nil
}

// applyAccounts builds the new account list: each account keeps its history
// and last read by id. An existing id cannot change provider: its history
// would be read as another provider's.
func applyAccounts(current []store.Account, next []accountSettings) ([]store.Account, error) {
	byID := map[string]store.Account{}
	for _, a := range current {
		byID[a.ID] = a
	}
	out := make([]store.Account, 0, len(next))
	for i, n := range next {
		account := store.Account{History: map[string]history.State{}}
		if old, ok := byID[n.ID]; ok {
			if old.Provider != n.Provider {
				return nil, invalid(fmt.Sprintf("accounts[%d].provider", i),
					"account %q is a %s account: remove it and add a new one", n.ID, old.Provider)
			}
			account = old
		}
		account.ID, account.Provider, account.Label, account.Enabled = n.ID, n.Provider, n.Label, n.Enabled
		account.RenewalDay = n.RenewalDay
		account.Schedule = store.Schedule{Enabled: n.Schedule.Enabled, HoursPerDay: n.Schedule.HoursPerDay}
		copy(account.Schedule.Days[:], n.Schedule.Days)
		account.Host, account.Scope, account.ManualQuota, account.BinaryPath = n.Host, n.Scope, n.ManualQuota, n.BinaryPath
		out = append(out, account)
	}
	return out, nil
}

// changedAccounts are the ids, sorted, of the accounts added, removed,
// enabled, disabled or read differently (store.Account.ReadsLike). A label,
// schedule or renewal day does not change a read.
func changedAccounts(before, after []store.Account) []string {
	old := map[string]store.Account{}
	for _, a := range before {
		old[a.ID] = a
	}
	var changed []string
	for _, a := range after {
		prev, ok := old[a.ID]
		if !ok || prev.Enabled != a.Enabled || !prev.ReadsLike(a) {
			changed = append(changed, a.ID)
		}
		delete(old, a.ID)
	}
	for id := range old {
		changed = append(changed, id)
	}
	slices.Sort(changed)
	return changed
}

func writeSettings(w http.ResponseWriter, s store.State) {
	doc := settingsDocument{SchemaVersion: usage.SchemaVersion, Settings: s.Settings, Accounts: make([]accountSettings, 0, len(s.Accounts))}
	for _, a := range s.Accounts {
		binary := a.BinaryPath
		if binary != nil && *binary == "" {
			binary = nil
		}
		doc.Accounts = append(doc.Accounts, accountSettings{
			ID: a.ID, Provider: a.Provider, Label: a.Label, Enabled: a.Enabled, RenewalDay: a.RenewalDay,
			Schedule: scheduleSettings{Enabled: a.Schedule.Enabled, Days: a.Schedule.Days[:], HoursPerDay: a.Schedule.HoursPerDay},
			Host:     a.Host, Scope: a.Scope, ManualQuota: a.ManualQuota, BinaryPath: binary,
		})
	}
	data, _ := json.Marshal(doc) // plain data: cannot fail
	writeDocument(w, http.StatusOK, append(data, '\n'))
}

func writeInvalidSettings(w http.ResponseWriter, status int, message string) {
	var buf bytes.Buffer
	_ = usage.WriteError(&buf, "", &usage.Error{Code: usage.CodeUsage, Message: message, Reason: reasonInvalidSettings})
	writeDocument(w, status, buf.Bytes())
}
