package logging_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/logging"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		level   slog.Level
		warning bool
	}{
		{"default", "", slog.LevelWarn, false},
		{"debug", "debug", slog.LevelDebug, false},
		{"info", "info", slog.LevelInfo, false},
		{"warn", "warn", slog.LevelWarn, false},
		{"error", "error", slog.LevelError, false},
		{"invalid", "invalid", slog.LevelWarn, true},
		{"uppercase is invalid", "DEBUG", slog.LevelWarn, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AIPERMILER_LOG_LEVEL", tt.value)
			var output bytes.Buffer
			logger := logging.New(&output)
			for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
				if got := logger.Enabled(context.Background(), level); got != (level >= tt.level) {
					t.Errorf("Enabled(%v) = %v; want %v", level, got, level >= tt.level)
				}
			}
			if tt.warning {
				if strings.Count(output.String(), "level=WARN") != 1 || !strings.Contains(output.String(), "Invalid AIPERMILER_LOG_LEVEL; using warn") {
					t.Errorf("expected one fallback warning; got %q", output.String())
				}
			} else if output.Len() != 0 {
				t.Errorf("unexpected startup log: %q", output.String())
			}
			output.Reset()
			logger.Debug("debug message")
			logger.Info("info message")
			logger.Warn("warn message")
			logger.Error("error message")
			for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
				if got := strings.Contains(output.String(), "level="+level.String()); got != (level >= tt.level) {
					t.Errorf("written level %v = %v; want %v", level, got, level >= tt.level)
				}
			}
		})
	}
}
