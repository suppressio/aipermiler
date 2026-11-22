//go:build windows

package service_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/suppressio/aipermiler/gocore/internal/service"
)

// holdWithoutDeleteSharing opens path the way .NET and PowerShell do by
// default (FILE_SHARE_READ only): until the handle closes, Windows refuses to
// replace or delete the file. The handle is closed after d.
func holdWithoutDeleteSharing(t *testing.T, path string, d time.Duration) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(d)
		_ = windows.CloseHandle(h)
	}()
}

// A reader that holds api.json for a moment must not make the start fail (#292).
func TestWriteInfoWaitsForShortReader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := service.WriteInfo(dir, sampleInfo()); err != nil {
		t.Fatal(err)
	}
	holdWithoutDeleteSharing(t, filepath.Join(dir, "api.json"), 300*time.Millisecond)
	if err := service.WriteInfo(dir, sampleInfo()); err != nil {
		t.Fatalf("WriteInfo with api.json held for 300 ms: %v", err)
	}
}

// Nor the stop leave api.json behind (#292).
func TestRemoveInfoWaitsForShortReader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := service.WriteInfo(dir, sampleInfo()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "api.json")
	holdWithoutDeleteSharing(t, path, 300*time.Millisecond)
	if err := service.RemoveInfo(dir); err != nil {
		t.Fatalf("RemoveInfo with api.json held for 300 ms: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("api.json still there: %v", err)
	}
}
