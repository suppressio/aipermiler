package electronimport_test

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/electronimport"
	"github.com/suppressio/aipermiler/gocore/internal/store"
)

func setup(t *testing.T, electronFile []byte) (storeDir, electronDir string) {
	t.Helper()
	storeDir, electronDir = t.TempDir(), t.TempDir()
	if electronFile != nil {
		if err := os.WriteFile(filepath.Join(electronDir, electronimport.FileName), electronFile, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return storeDir, electronDir
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestImportWritesTheStoreOnce(t *testing.T) {
	t.Parallel()
	enc := fixture(t, "iv-truncated-3byte.bin")
	storeDir, electronDir := setup(t, enc)
	if err := electronimport.Import(storeDir, electronDir, quiet()); err != nil {
		t.Fatalf("Import: %v", err)
	}
	s, err := store.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); len(got.Accounts) != 3 || got.Settings.RefreshIntervalMinutes != 15 {
		t.Fatalf("state = %+v", got)
	}
	after, err := os.ReadFile(filepath.Join(electronDir, electronimport.FileName))
	if err != nil || !bytes.Equal(after, enc) {
		t.Fatalf("electron file changed: %v", err)
	}
}

func TestImportSkipsWhenTheStoreExists(t *testing.T) {
	t.Parallel()
	storeDir, electronDir := setup(t, fixture(t, "iv-ascii.bin"))
	existing := []byte(`{"schemaVersion":1,"settings":{},"accounts":[]}`)
	if err := os.WriteFile(filepath.Join(storeDir, store.FileName), existing, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := electronimport.Import(storeDir, electronDir, quiet()); err != nil {
		t.Fatalf("Import: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(storeDir, store.FileName))
	if !bytes.Equal(got, existing) {
		t.Fatalf("store changed: %s", got)
	}
}

func TestImportSkipsWithoutElectronFile(t *testing.T) {
	t.Parallel()
	storeDir, electronDir := setup(t, nil)
	if err := electronimport.Import(storeDir, electronDir, quiet()); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeDir, store.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("store.json exists: %v", err)
	}
}

func TestFailedImportWritesNothing(t *testing.T) {
	t.Parallel()
	for name, data := range map[string][]byte{
		"older format": bytes.Repeat([]byte{0x41}, 48),
		"garbage":      []byte("not an encrypted file at all"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			storeDir, electronDir := setup(t, data)
			err := electronimport.Import(storeDir, electronDir, quiet())
			if err == nil {
				t.Fatal("Import succeeded")
			}
			if _, statErr := os.Stat(filepath.Join(storeDir, store.FileName)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("store.json written: %v", statErr)
			}
			left, _ := os.ReadFile(filepath.Join(electronDir, electronimport.FileName))
			if !bytes.Equal(left, data) {
				t.Fatal("electron file changed")
			}
		})
	}
}

func TestDefaultDirIsElectronUserData(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	t.Setenv("HOME", "/home/u")
	t.Setenv("AppData", `C:\Users\u\AppData\Roaming`)
	dir, err := electronimport.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "AIpermiler" {
		t.Fatalf("dir = %s", dir)
	}
}

func TestDefaultDirDevOverride(t *testing.T) {
	override := t.TempDir()
	t.Setenv(electronimport.DevDirEnv, override)
	dir, err := electronimport.DefaultDir()
	if err != nil || dir != override {
		t.Fatalf("dir = %q, %v", dir, err)
	}
	t.Setenv(electronimport.DevDirEnv, "relative/dir")
	if _, err := electronimport.DefaultDir(); err == nil {
		t.Fatal("a relative override was accepted")
	}
}
