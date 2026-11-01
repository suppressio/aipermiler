package secrets_test

import (
	"errors"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/secrets"
)

func TestValidAccount(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]bool{
		"copilot": true, "copilot-1a2b3c4d": true, "a.b_c-1": true,
		"": false, "copilot/other": false, "../x": false, "a b": false, string(make([]byte, 129)): false,
	} {
		if got := secrets.ValidAccount(id); got != want {
			t.Errorf("ValidAccount(%q) = %v; want %v", id, got, want)
		}
	}
}

func TestMemory(t *testing.T) {
	t.Parallel()
	var m secrets.Memory
	if _, err := m.Get("k"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get on empty = %v; want ErrNotFound", err)
	}
	if err := m.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, err := m.Get("k"); err != nil || v != "v" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := m.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("k"); err != nil {
		t.Fatalf("second Delete = %v; want nil", err)
	}
	m.Err = errors.New("no keyring")
	if _, err := m.Get("k"); err == nil {
		t.Fatal("Get with Err set returned nil")
	}
}

// failingDelete is a Memory whose Delete always fails (a locked legacy entry).
type failingDelete struct{ secrets.Memory }

func (*failingDelete) Delete(string) error { return errors.New("delete refused") }

func TestMigratingGet(t *testing.T) {
	t.Parallel()

	t.Run("current wins, legacy untouched", func(t *testing.T) {
		t.Parallel()
		current, legacy := &secrets.Memory{}, &secrets.Memory{}
		mustSet(t, current, "new")
		mustSet(t, legacy, "old")
		if v, err := (secrets.Migrating{Current: current, Legacy: legacy}).Get("k"); err != nil || v != "new" {
			t.Fatalf("Get = %q, %v; want new", v, err)
		}
		if v, _ := legacy.Get("k"); v != "old" {
			t.Fatalf("legacy = %q; want it untouched", v)
		}
	})

	t.Run("only in legacy: moved", func(t *testing.T) {
		t.Parallel()
		current, legacy := &secrets.Memory{}, &secrets.Memory{}
		mustSet(t, legacy, "old")
		if v, err := (secrets.Migrating{Current: current, Legacy: legacy}).Get("k"); err != nil || v != "old" {
			t.Fatalf("Get = %q, %v; want old", v, err)
		}
		if v, err := current.Get("k"); err != nil || v != "old" {
			t.Fatalf("current = %q, %v; want the moved value", v, err)
		}
		if _, err := legacy.Get("k"); !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("legacy Get = %v; want ErrNotFound after the move", err)
		}
	})

	t.Run("in neither", func(t *testing.T) {
		t.Parallel()
		_, err := (secrets.Migrating{Current: &secrets.Memory{}, Legacy: &secrets.Memory{}}).Get("k")
		if !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("Get = %v; want ErrNotFound", err)
		}
	})

	t.Run("legacy delete fails: value returned, copy kept", func(t *testing.T) {
		t.Parallel()
		current, legacy := &secrets.Memory{}, &failingDelete{}
		mustSet(t, &legacy.Memory, "old")
		if v, err := (secrets.Migrating{Current: current, Legacy: legacy}).Get("k"); err != nil || v != "old" {
			t.Fatalf("Get = %q, %v; want old", v, err)
		}
		if v, _ := current.Get("k"); v != "old" {
			t.Fatalf("current = %q; want old", v)
		}
	})

	t.Run("current set fails: value returned, legacy kept", func(t *testing.T) {
		t.Parallel()
		current, legacy := &secrets.Memory{}, &secrets.Memory{}
		mustSet(t, legacy, "old")
		current.Err = secrets.ErrNotFound // Get says "not found", Set fails
		if v, err := (secrets.Migrating{Current: current, Legacy: legacy}).Get("k"); err != nil || v != "old" {
			t.Fatalf("Get = %q, %v; want old", v, err)
		}
		if v, _ := legacy.Get("k"); v != "old" {
			t.Fatalf("legacy = %q; want it kept for the next try", v)
		}
	})
}

func TestMigratingSetAndDelete(t *testing.T) {
	t.Parallel()
	current, legacy := &secrets.Memory{}, &secrets.Memory{}
	m := secrets.Migrating{Current: current, Legacy: legacy}
	mustSet(t, legacy, "old")
	if err := m.Set("k", "new"); err != nil {
		t.Fatal(err)
	}
	if v, _ := legacy.Get("k"); v != "old" {
		t.Fatalf("Set touched legacy: %q", v)
	}
	if err := m.Delete("k"); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*secrets.Memory{"current": current, "legacy": legacy} {
		if _, err := s.Get("k"); !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("%s Get after Delete = %v; want ErrNotFound", name, err)
		}
	}
}

func mustSet(t *testing.T, s secrets.Store, value string) {
	t.Helper()
	if err := s.Set("k", value); err != nil {
		t.Fatal(err)
	}
}
