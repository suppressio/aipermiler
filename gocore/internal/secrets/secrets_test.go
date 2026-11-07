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
