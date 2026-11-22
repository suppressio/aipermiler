package electronimport

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/store"
)

// Import copies Electron's settings and history into the core's store, once:
// only when storeDir has no store.json and electronDir has Electron's file.
// It must run in the service, holding service.lock, the store's only writer.
//
// The store is written in one atomic commit, complete or not at all. Whatever
// fails is returned and nothing is written, so the caller starts with an empty
// store (the next start tries again, as store.json is still missing). Electron's
// file is only ever read.
func Import(storeDir, electronDir string, logger *slog.Logger) error {
	if _, err := os.Stat(filepath.Join(storeDir, store.FileName)); err == nil {
		return nil // already imported, or created by the service
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check %s: %w", store.FileName, err)
	}
	data, err := os.ReadFile(filepath.Join(electronDir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil // a fresh install: nothing to import
	}
	if err != nil {
		return fmt.Errorf("read electron-store file: %w", err)
	}
	plain, err := Decrypt(data, AppKey)
	if err != nil {
		return err
	}
	state, report, err := Convert(plain, time.Local)
	if err != nil {
		return err
	}
	s, err := store.Open(storeDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	err = s.Update(func(st *store.State) error {
		st.Settings, st.Accounts = state.Settings, state.Accounts
		return nil
	})
	if err != nil && !errors.Is(err, store.ErrNotSynced) {
		return fmt.Errorf("write store: %w", err)
	}
	for _, w := range report.Warnings {
		logger.Warn("Import left something out", "what", w)
	}
	logger.Info("Imported the app's settings and history from electron-store",
		"accounts", len(state.Accounts), "skipped", report.Skipped)
	if err != nil {
		logger.Warn("The imported store may not survive a power loss", "error", err)
	}
	return nil
}
