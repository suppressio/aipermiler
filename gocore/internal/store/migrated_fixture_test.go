package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/store"
)

// TestMigratedFixtureIsAStore pins the contract with the migration script
// (scripts/migrate-core-store, #311): the store.json it writes, the same file
// its JS test compares against, must open as a store and survive a round trip
// unchanged. A field the script adds, misses or formats differently (a time
// in another layout, null instead of omitted) fails here, so a schema change in
// Go fails until the script follows.
func TestMigratedFixtureIsAStore(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "migrated-from-electron.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, store.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	again, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(again, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("the fixture does not round-trip through the store:\nfile:  %s\nstore: %s", data, again)
	}
	if n := len(s.Snapshot().Accounts); n != 3 {
		t.Errorf("accounts = %d, want 3", n)
	}
}
