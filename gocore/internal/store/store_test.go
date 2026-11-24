package store_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

var day = time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)

func historySettings() history.Settings {
	return history.Settings{Location: time.UTC, RetentionDays: 90}
}

func sample(minutes int, used float64) history.Sample {
	return history.Sample{At: day.Add(time.Duration(minutes) * time.Minute), Used: used, Fresh: true}
}

// ReadsLike compares what a read depends on: id, provider and provider flags,
// not the label, schedule, renewal day, history or enabled state (#284).
func TestReadsLike(t *testing.T) {
	t.Parallel()
	path := "/opt/claude"
	base := store.Account{ID: "a", Provider: "copilot", Label: "A", Enabled: true, Host: "github.com", Scope: "personal", ManualQuota: 300}
	same := base
	same.Label, same.RenewalDay, same.Enabled = "B", 5, false
	same.Schedule.Enabled = true
	if !base.ReadsLike(same) {
		t.Error("label, renewal day, schedule or enabled changed the read")
	}
	empty, unset := base, base
	empty.BinaryPath = new(string)
	unset.BinaryPath = nil
	if !empty.ReadsLike(unset) {
		t.Error("an empty binary path differs from none")
	}
	for name, change := range map[string]func(*store.Account){
		"provider":    func(a *store.Account) { a.Provider = "claude" },
		"host":        func(a *store.Account) { a.Host = "x.ghe.com" },
		"scope":       func(a *store.Account) { a.Scope = "organization" },
		"manualQuota": func(a *store.Account) { a.ManualQuota = 1500 },
		"binaryPath":  func(a *store.Account) { a.BinaryPath = &path },
		"id":          func(a *store.Account) { a.ID = "b" },
	} {
		other := base
		change(&other)
		if base.ReadsLike(other) {
			t.Errorf("a change of %s does not change the read", name)
		}
	}
}

func mustOpen(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func addAccount(t *testing.T, s *store.Store, id string) {
	t.Helper()
	err := s.Update(func(st *store.State) error {
		st.Accounts = append(st.Accounts, store.Account{ID: id, Provider: "claude", Enabled: true})
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestFileNameDoesNotClashWithElectron(t *testing.T) {
	t.Parallel()
	if strings.EqualFold(store.FileName, "aipermiler-config.json") {
		t.Fatalf("%s clashes with electron-store's file", store.FileName)
	}
}

func TestMissingFileIsEmptyStoreAndWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := mustOpen(t, dir)
	got := s.Snapshot()
	if got.SchemaVersion != store.SchemaVersion || len(got.Accounts) != 0 || got.Settings.RetentionDays != 90 {
		t.Fatalf("empty store = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, store.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open created the file: %v", err)
	}
}

func TestUpdatePersistsAndReopens(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := mustOpen(t, dir)
	renews := day.Add(48 * time.Hour)
	total := 100.0
	err := s.Update(func(st *store.State) error {
		st.Settings.RefreshIntervalMinutes = 15
		st.Accounts = []store.Account{{
			ID: "a1", Provider: "claude", Label: "Work", Enabled: true, RenewalDay: 5,
			Schedule: store.Schedule{Enabled: true, Days: [7]float64{0, 1, 1, 1, 1, 0.5, 0}, HoursPerDay: 7.5},
		}}
		a := st.Account("a1")
		a.Record("weekly", sample(0, 12.345678901234567), day, historySettings())
		a.LastGood = &store.LastGood{
			UpdatedAt: day, SubscriptionRenewsAt: &renews,
			QuotaWindows: []store.Window{{ID: "weekly", PeriodType: usage.PeriodRollingDays, Unit: usage.UnitPercentage, Used: 12.3, Total: &total, ResetsAt: &renews}},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	reopened := mustOpen(t, dir).Snapshot()
	if !reflect.DeepEqual(reopened, s.Snapshot()) {
		t.Fatalf("reopened differs:\n%+v\n%+v", reopened, s.Snapshot())
	}
	a := reopened.Account("a1")
	if a == nil || a.History["weekly"].DailyUsage[0].Used != 12.345678901234567 {
		t.Fatalf("history lost precision: %+v", a)
	}
	if got := a.LastGood.Usage().QuotaWindows[0].ResetsAt; got == nil || !got.Equal(renews) {
		t.Fatalf("lastGood resetsAt = %v", got)
	}
	info, err := os.Stat(filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 && filepath.Separator == '/' {
		t.Fatalf("mode = %v, want owner only", info.Mode().Perm())
	}
}

func TestOpenErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, content string
		want          error
	}{
		{"not json", "{nope", store.ErrUnreadable},
		{"truncated", `{"schemaVersion":1,"accounts":[`, store.ErrUnreadable},
		{"no schema version", `{"accounts":[]}`, store.ErrUnreadable},
		{"newer schema", `{"schemaVersion":2,"accounts":[]}`, store.ErrNewerSchema},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, store.FileName)
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Open(dir); !errors.Is(err, tt.want) {
				t.Fatalf("Open = %v, want %v", err, tt.want)
			}
			if got, _ := os.ReadFile(path); string(got) != tt.content {
				t.Fatalf("Open modified the file: %q", got)
			}
		})
	}
}

func TestOpenUnreadableFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A directory where the file should be: ReadFile fails with something other than not-exist.
	if err := os.Mkdir(filepath.Join(dir, store.FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(dir); !errors.Is(err, store.ErrUnreadable) {
		t.Fatalf("Open = %v, want ErrUnreadable", err)
	}
}

func TestOlderFileKeepsDefaultsOfMissingFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := `{"schemaVersion":1,"accounts":[{"id":"a","provider":"codex","enabled":true}],"futureField":1}`
	if err := os.WriteFile(filepath.Join(dir, store.FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := mustOpen(t, dir).Snapshot()
	if got.Settings.RefreshIntervalMinutes != 30 || got.Account("a") == nil {
		t.Fatalf("state = %+v", got)
	}
}

func TestFailedUpdateLeavesNoHalfUpdatedDay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := mustOpen(t, dir)
	addAccount(t, s, "a1")
	if err := s.Update(func(st *store.State) error {
		st.Account("a1").Record("w", sample(0, 10), day, historySettings())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, store.FileName)
	before, _ := os.ReadFile(path)
	want := s.Snapshot()

	boom := errors.New("crash between computing and saving")
	err := s.Update(func(st *store.State) error {
		st.Account("a1").Record("w", sample(30, 40), day.Add(time.Hour), historySettings())
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update = %v", err)
	}
	assertUnchanged(t, s, path, before, want)

	func() {
		defer func() { _ = recover() }()
		_ = s.Update(func(st *store.State) error {
			st.Account("a1").Record("w", sample(30, 40), day.Add(time.Hour), historySettings())
			panic("crash")
		})
	}()
	assertUnchanged(t, s, path, before, want)
	// The lock was released by the panic: the store still works.
	addAccount(t, s, "a2")
}

func assertUnchanged(t *testing.T, s *store.Store, path string, before []byte, want store.State) {
	t.Helper()
	if got, _ := os.ReadFile(path); string(got) != string(before) {
		t.Fatalf("file changed:\n%s", got)
	}
	if !reflect.DeepEqual(s.Snapshot(), want) {
		t.Fatalf("memory changed: %+v", s.Snapshot())
	}
	if !reflect.DeepEqual(mustOpen(t, filepath.Dir(path)).Snapshot(), want) {
		t.Fatal("reopened state differs")
	}
}

func TestFailedWriteKeepsOldFileAndState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := mustOpen(t, dir)
	addAccount(t, s, "a1")
	path := filepath.Join(dir, store.FileName)
	before, _ := os.ReadFile(path)
	want := s.Snapshot()

	// Make the rename fail: the target becomes a non-empty directory.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := s.Update(func(st *store.State) error {
		st.Settings.RefreshIntervalMinutes = 5
		return nil
	})
	if err == nil {
		t.Fatal("Update succeeded over an unwritable target")
	}
	if !reflect.DeepEqual(s.Snapshot(), want) {
		t.Fatalf("memory changed after a failed write: %+v", s.Snapshot())
	}
	if len(before) == 0 {
		t.Fatal("test setup")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// After the rename the new file is in place: a failed folder sync must not
// leave the memory behind it, or the next write would revert this change.
func TestFolderSyncFailureStillCommits(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the folder is not synced on Windows; root ignores the permission")
	}
	dir := t.TempDir()
	s := mustOpen(t, dir)
	// Write and search but no read: the rename works, opening the folder to sync it fails.
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	for _, minutes := range []int{5, 10} {
		err := s.Update(func(st *store.State) error {
			if minutes == 5 {
				st.Settings.RefreshIntervalMinutes = minutes
			} else {
				st.Settings.RetentionDays = minutes
			}
			return nil
		})
		if !errors.Is(err, store.ErrNotSynced) {
			t.Fatalf("Update = %v, want ErrNotSynced", err)
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	got := mustOpen(t, dir).Snapshot().Settings
	if got.RefreshIntervalMinutes != 5 || got.RetentionDays != 10 {
		t.Fatalf("file settings = %+v, want both changes", got)
	}
	if !reflect.DeepEqual(s.Snapshot().Settings, got) {
		t.Fatalf("memory %+v differs from the file %+v", s.Snapshot().Settings, got)
	}
}

// What fn puts into the state is copied: a map the caller keeps is not the store's.
func TestUpdateKeepsNoReferenceToTheCallersData(t *testing.T) {
	t.Parallel()
	s := mustOpen(t, t.TempDir())
	h := map[string]history.State{}
	if err := s.Update(func(st *store.State) error {
		st.Accounts = append(st.Accounts, store.Account{ID: "a1", History: h})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h["leak"] = history.State{}
	if _, ok := s.Snapshot().Account("a1").History["leak"]; ok {
		t.Fatal("the store shares the caller's map")
	}
}

func TestLeftoverTempFileIsIgnoredAndRemovable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := mustOpen(t, dir)
	addAccount(t, s, "a1")
	want := s.Snapshot()
	leftover := filepath.Join(dir, store.FileName+".123.tmp")
	other := filepath.Join(dir, "other.tmp")
	for _, p := range []string{leftover, other} {
		if err := os.WriteFile(p, []byte("{half"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustOpen(t, dir).Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("a leftover changed the state: %+v", got)
	}
	if err := store.RemoveLeftovers(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover kept: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("foreign file removed: %v", err)
	}
	if err := store.RemoveLeftovers(filepath.Join(dir, "missing")); err != nil {
		t.Fatalf("missing folder: %v", err)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	t.Parallel()
	s := mustOpen(t, t.TempDir())
	addAccount(t, s, "a1")
	snap := s.Snapshot()
	snap.Account("a1").Label = "changed"
	snap.Settings.RetentionDays = 1
	if got := s.Snapshot(); got.Account("a1").Label != "" || got.Settings.RetentionDays != 90 {
		t.Fatalf("Snapshot shares memory: %+v", got)
	}
}

// Overlapping refreshes (timer, manual, wake from sleep) end in the history a
// sequential run in the order they were applied produces, and none is lost.
func TestOverlappingRefreshesEqualSequentialOnes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := mustOpen(t, dir)
	addAccount(t, s, "a1")
	const refreshes = 40

	var (
		orderMu sync.Mutex
		order   []int
		wg      sync.WaitGroup
	)
	for i := 1; i <= refreshes; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Update(func(st *store.State) error {
				orderMu.Lock()
				order = append(order, i)
				orderMu.Unlock()
				st.Account("a1").Record("w", sample(i, float64(i)), day.Add(refreshes*time.Minute), historySettings())
				return nil
			})
			if err != nil {
				t.Errorf("Update %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	var want history.State
	for _, i := range order {
		want = history.Record(want, sample(i, float64(i)), day.Add(refreshes*time.Minute), historySettings())
	}
	got := s.Snapshot().Account("a1").History["w"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("concurrent history differs from the sequential replay of %v:\n%+v\n%+v", order, got, want)
	}
	if len(got.DailyUsage) != 1 {
		t.Fatalf("daily points = %d, want 1", len(got.DailyUsage))
	}
	if reopened := mustOpen(t, dir).Snapshot().Account("a1").History["w"]; !reflect.DeepEqual(reopened, want) {
		t.Fatal("file differs from memory")
	}
}

func TestConcurrentUpdatesOfDifferentAccountsAreAllKept(t *testing.T) {
	t.Parallel()
	s := mustOpen(t, t.TempDir())
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("a%d", i)
			if err := s.Update(func(st *store.State) error {
				st.Accounts = append(st.Accounts, store.Account{ID: id})
				return nil
			}); err != nil {
				t.Errorf("Update: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := len(s.Snapshot().Accounts); got != 20 {
		t.Fatalf("accounts = %d, want 20", got)
	}
}
