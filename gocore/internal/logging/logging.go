// Package logging constructs stderr loggers without changing the global logger.
package logging

import (
	"io"
	"log/slog"
	"os"
)

// New uses AIPERMILER_LOG_LEVEL, defaulting to warn for unset or invalid values.
func New(w io.Writer) *slog.Logger {
	value := os.Getenv("AIPERMILER_LOG_LEVEL")
	level := slog.LevelWarn
	valid := true
	switch value {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "", "warn":
	case "error":
		level = slog.LevelError
	default:
		valid = false
	}
	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
	if !valid {
		logger.Warn("Invalid AIPERMILER_LOG_LEVEL; using warn")
	}
	return logger
}
