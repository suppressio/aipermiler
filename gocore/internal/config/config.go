// Package config locates and creates the core's configuration directory.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
)

// Dir returns the platform's config directory or an absolute development override.
func Dir() (string, error) {
	if override := os.Getenv("AIPERMILER_CONFIG_DIR"); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("AIPERMILER_CONFIG_DIR must be an absolute path")
		}
		return override, nil
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	return filepath.Join(base, buildinfo.Name), nil
}

// Ensure creates the config directory with owner-only permissions on Unix.
func Ensure() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", fmt.Errorf("resolve config directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	return dir, nil
}
