package service_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/service"
)

func sampleInfo() service.Info {
	return service.Info{
		SchemaVersion: 1, Port: 4242, Token: "tok", Version: "1.2.3", ContractVersion: 1,
		PID: 77, StartedAt: time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC),
	}
}

func TestWriteInfoDocument(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := service.WriteInfo(dir, sampleInfo()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "api.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"schemaVersion": 1.0, "port": 4242.0, "token": "tok", "version": "1.2.3",
		"contractVersion": 1.0, "pid": 77.0, "startedAt": "2026-11-19T21:00:00Z",
	}
	if len(got) != len(want) {
		t.Fatalf("fields = %v; want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v; want %v", k, got[k], v)
		}
	}
}

func TestWriteInfoIsPrivateAndAtomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := service.WriteInfo(dir, sampleInfo()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, "api.json"))
		if err != nil {
			t.Fatal(err)
		}
		if mode := st.Mode().Perm(); mode != 0o600 {
			t.Errorf("mode = %o; want 600", mode)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory has %d entries after the write; the temp file must be gone", len(entries))
	}
}

func TestWriteInfoOverwritesStaleFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "api.json"), []byte(`{"port":1,"token":"old"}`), 0o644); err != nil { //nolint:gosec // a stale file with loose permissions is the case under test
		t.Fatal(err)
	}
	if err := service.WriteInfo(dir, sampleInfo()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "api.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got service.Info
	if err := json.Unmarshal(raw, &got); err != nil || got.Token != "tok" || got.Port != 4242 {
		t.Fatalf("stale file not replaced: %s (%v)", raw, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(filepath.Join(dir, "api.json"))
		if mode := st.Mode().Perm(); mode != 0o600 {
			t.Errorf("mode after overwrite = %o; want 600", mode)
		}
	}
}

func TestRemoveInfo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := service.RemoveInfo(dir); err != nil {
		t.Fatalf("RemoveInfo with no file: %v", err)
	}
	if err := service.WriteInfo(dir, sampleInfo()); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveInfo(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "api.json")); !os.IsNotExist(err) {
		t.Fatalf("api.json still there: %v", err)
	}
}
