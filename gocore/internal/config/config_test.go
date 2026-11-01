package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
	"github.com/suppressio/aipermiler/gocore/internal/config"
)

func TestDir(t *testing.T) {
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(t.TempDir(), "config")
	tests := []struct {
		name     string
		override string
		want     string
		wantErr  bool
	}{
		{"platform default", "", filepath.Join(base, buildinfo.Name), false},
		{"absolute override", override, override, false},
		{"relative override", filepath.Join("relative", "config"), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AIPERMILER_CONFIG_DIR", tt.override)
			got, err := config.Dir()
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("Dir() = %q, %v; want %q, error %v", got, err, tt.want, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "AIPERMILER_CONFIG_DIR must be an absolute path") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

//nolint:paralleltest // uses t.Setenv
func TestEnsure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "config")
	t.Setenv("AIPERMILER_CONFIG_DIR", dir)
	for _, name := range []string{"create", "already exists"} {
		t.Run(name, func(t *testing.T) {
			got, err := config.Ensure()
			if err != nil || got != dir {
				t.Fatalf("Ensure() = %q, %v; want %q, nil", got, err, dir)
			}
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsDir() {
				t.Fatal("Ensure() did not create a directory")
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
				t.Errorf("permissions = %o; want 700", info.Mode().Perm())
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Errorf("config directory is not empty: %v, %v", entries, err)
			}
		})
	}
}

func TestEnsureErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		override string
		wantText string
		wantPath bool
	}{
		{"relative path", "relative", "resolve config directory:", false},
		{"existing file", file, "create config directory:", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AIPERMILER_CONFIG_DIR", tt.override)
			got, err := config.Ensure()
			if err == nil || got != "" {
				t.Fatalf("Ensure() = %q, %v; want empty path and error", got, err)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %q; want %q", err, tt.wantText)
			}
			var pathErr *os.PathError
			if tt.wantPath && !errors.As(err, &pathErr) {
				t.Errorf("error does not wrap *os.PathError: %v", err)
			}
		})
	}
}
