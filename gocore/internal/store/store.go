// Package store is the core's persisted state and its only writer: one JSON
// file in the config folder, replaced atomically on every change.
//
// A Store serializes every change through Update, so overlapping refreshes
// (timer, manual, wake from sleep) apply one after the other. It does not lock
// the file against other processes: the service holds the config folder's lock
// (internal/service), and only it opens a Store for writing.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const (
	// FileName is the state file. It must not clash with Electron's
	// aipermiler-config.json: on Windows and macOS the two config folders are
	// the same one, on case-insensitive file systems.
	FileName = "store.json"
	// tempPattern is the os.CreateTemp pattern of the temp files of a write.
	tempPattern = FileName + ".*.tmp"
	// SchemaVersion is the version of the file layout. Additive fields keep it,
	// but an older core drops them at its next write: a field whose loss after a
	// downgrade matters bumps it.
	SchemaVersion = 1
)

// ErrNewerSchema means the file was written by a newer core: opening it
// read-write could drop what this core does not know.
var ErrNewerSchema = errors.New("store file has a newer schema version")

// ErrUnreadable means the file exists but is not a valid store. It is never
// reset silently: the user's history would be lost.
var ErrUnreadable = errors.New("store file is unreadable")

// ErrNotSynced means the change is committed (file renamed, memory updated) but
// the folder sync failed, so a power loss could still bring the old file back.
var ErrNotSynced = errors.New("store change committed but its folder was not synced")

// Settings are the global settings the core needs.
type Settings struct {
	RefreshIntervalMinutes       int `json:"refreshIntervalMinutes"`
	RetentionDays                int `json:"retentionDays"`
	NotificationThresholdPercent int `json:"notificationThresholdPercent"`
}

// Schedule is an account's work schedule. Days are Sunday first, with weights
// 0, 0.5 or 1, as quotaperiod.Schedule.
type Schedule struct {
	Enabled     bool       `json:"enabled"`
	Days        [7]float64 `json:"days"`
	HoursPerDay float64    `json:"hoursPerDay"`
}

// Window is a quota window as last read, mirroring usage.QuotaWindow.
type Window struct {
	ID           string           `json:"id"`
	Label        string           `json:"label"`
	PeriodType   usage.PeriodType `json:"periodType"`
	PeriodLength *float64         `json:"periodLength"`
	Unit         usage.Unit       `json:"unit"`
	Used         float64          `json:"used"`
	Total        *float64         `json:"total"`
	ResetsAt     *time.Time       `json:"resetsAt"`
}

// LastGood is the last successful read of an account, shown as stale when a
// later fetch fails.
type LastGood struct {
	UpdatedAt            time.Time  `json:"updatedAt"`
	PlanTier             *string    `json:"planTier"`
	SubscriptionRenewsAt *time.Time `json:"subscriptionRenewsAt"`
	QuotaWindows         []Window   `json:"quotaWindows"`
}

// NewLastGood converts a successful provider read.
func NewLastGood(u usage.Usage, at time.Time) LastGood {
	windows := make([]Window, 0, len(u.QuotaWindows))
	for _, w := range u.QuotaWindows {
		windows = append(windows, Window{
			ID: w.ID, Label: w.Label, PeriodType: w.PeriodType, PeriodLength: w.PeriodLength,
			Unit: w.Unit, Used: w.Used, Total: w.Total, ResetsAt: w.ResetsAt,
		})
	}
	return LastGood{UpdatedAt: at, PlanTier: u.PlanTier, SubscriptionRenewsAt: u.SubscriptionRenewsAt, QuotaWindows: windows}
}

// Usage converts back to the provider document.
func (l LastGood) Usage() usage.Usage {
	windows := make([]usage.QuotaWindow, 0, len(l.QuotaWindows))
	for _, w := range l.QuotaWindows {
		windows = append(windows, usage.QuotaWindow{
			ID: w.ID, Label: w.Label, PeriodType: w.PeriodType, PeriodLength: w.PeriodLength,
			Unit: w.Unit, Used: w.Used, Total: w.Total, ResetsAt: w.ResetsAt,
		})
	}
	return usage.Usage{PlanTier: l.PlanTier, SubscriptionRenewsAt: l.SubscriptionRenewsAt, QuotaWindows: windows}
}

// Account is one account's settings, history and last good read. Secrets are
// never here: the Copilot token is in the OS keyring (internal/secrets).
type Account struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
	// RenewalDay is the manual day-of-month renewal rule, 0 when none.
	RenewalDay int      `json:"renewalDay"`
	Schedule   Schedule `json:"schedule"`
	// Host, Scope and ManualQuota are Copilot's; BinaryPath is Claude's and
	// Codex's (nil lets the core find the binary).
	Host        string  `json:"host,omitempty"`
	Scope       string  `json:"scope,omitempty"`
	ManualQuota float64 `json:"manualQuota,omitempty"`
	BinaryPath  *string `json:"binaryPath,omitempty"`
	// History is per quota window id.
	History  map[string]history.State `json:"history"`
	LastGood *LastGood                `json:"lastGood"`
}

// Record applies one observation to a window's history through history.Record,
// the only way history changes.
func (a *Account) Record(windowID string, sample history.Sample, now time.Time, settings history.Settings) {
	if a.History == nil {
		a.History = map[string]history.State{}
	}
	a.History[windowID] = history.Record(a.History[windowID], sample, now, settings)
}

// State is the whole content of the file.
type State struct {
	SchemaVersion int       `json:"schemaVersion"`
	Settings      Settings  `json:"settings"`
	Accounts      []Account `json:"accounts"`
}

// Account returns the account with the id, or nil; the pointer aims into the
// state's slice, so changes through it change the state.
func (s State) Account(id string) *Account {
	for i := range s.Accounts {
		if s.Accounts[i].ID == id {
			return &s.Accounts[i]
		}
	}
	return nil
}

// Empty is the state of a missing file: the defaults of the app's settings.
func Empty() State {
	return State{
		SchemaVersion: SchemaVersion,
		Settings:      Settings{RefreshIntervalMinutes: 30, RetentionDays: 90, NotificationThresholdPercent: 80},
		Accounts:      []Account{},
	}
}

// Store holds the state in memory and on disk.
type Store struct {
	path string
	mu   sync.Mutex
	// state is replaced, never modified in place, so Snapshot can hand it out
	// after a copy only.
	state State
}

// Open reads dir's store file. A missing file is an empty store; an unreadable
// one is ErrUnreadable and a newer schema ErrNewerSchema.
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, FileName)
	state, err := load(path)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, state: state}, nil
}

func load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Empty(), nil
	}
	if err != nil {
		return State{}, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	var head struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return State{}, fmt.Errorf("%w: %s: %w", ErrUnreadable, FileName, err)
	}
	switch {
	case head.SchemaVersion > SchemaVersion:
		return State{}, fmt.Errorf("%w: %d, this core reads up to %d", ErrNewerSchema, head.SchemaVersion, SchemaVersion)
	case head.SchemaVersion < 1:
		return State{}, fmt.Errorf("%w: %s has no valid schemaVersion", ErrUnreadable, FileName)
	}
	// Decode over the defaults, so a field an older file lacks keeps its default.
	state := Empty()
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("%w: %s: %w", ErrUnreadable, FileName, err)
	}
	return state, nil
}

// Snapshot returns a copy of the state the caller may keep and modify.
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.state)
}

// Update runs fn on a copy of the state under the store's lock and commits it
// atomically. If fn fails or panics, or the write fails, neither the file nor
// the in-memory state changes: a half-updated day is never visible. Once the
// new file is in place the change is committed; a failed folder sync after
// that is reported as ErrNotSynced. What fn put into the state stays its own:
// the store keeps a copy.
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.state)
	if err := fn(&next); err != nil {
		return err
	}
	next.SchemaVersion = SchemaVersion
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", FileName, err)
	}
	err = writeAtomic(s.path, append(data, '\n'))
	if err != nil && !errors.Is(err, ErrNotSynced) {
		return err
	}
	s.state = decode(data) // what the file holds, sharing nothing with fn's values
	return err
}

func clone(s State) State {
	// A JSON round trip is a deep copy of exactly what is persisted.
	data, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("store: encode state: %v", err)) // plain data, cannot fail
	}
	return decode(data)
}

func decode(data []byte) State {
	out := Empty()
	if err := json.Unmarshal(data, &out); err != nil {
		panic(fmt.Sprintf("store: decode state: %v", err))
	}
	return out
}

// writeAtomic writes data to a temp file in the same folder, syncs it, renames
// it over path and syncs the folder, so a crash leaves the old or the new file.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, tempPattern) // created 0600
	if err != nil {
		return fmt.Errorf("create temp %s: %w", FileName, err)
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", FileName, err)
	}
	if runtime.GOOS != "windows" { // a directory cannot be opened for sync there
		d, err := os.Open(dir)
		if err == nil {
			err = d.Sync()
			if closeErr := d.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrNotSynced, err)
		}
	}
	return nil
}

// RemoveLeftovers deletes the temp files a crashed write left in dir. Call it
// only while holding the service lock: then every temp file belongs to a dead
// process.
func RemoveLeftovers(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list leftovers: %w", err)
	}
	var result error
	for _, e := range entries {
		if ok, _ := filepath.Match(tempPattern, e.Name()); ok {
			if rmErr := os.Remove(filepath.Join(dir, e.Name())); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				result = errors.Join(result, fmt.Errorf("remove leftover: %w", rmErr))
			}
		}
	}
	return result
}
