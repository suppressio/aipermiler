package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	infoName = "api.json"
	// tempPattern is the os.CreateTemp pattern of api.json's temp files; the
	// cleanup matches leftovers against the same pattern.
	tempPattern = infoName + ".*.tmp"
)

// SchemaVersion is the version of api.json and of the API's own documents.
const SchemaVersion = 1

// Info is the content of api.json: how a client reaches the running service.
// A client never trusts it alone: it connects and calls /health with the token.
type Info struct {
	SchemaVersion   int       `json:"schemaVersion"`
	Port            int       `json:"port"`
	Token           string    `json:"token"`
	Version         string    `json:"version"`
	ContractVersion int       `json:"contractVersion"`
	PID             int       `json:"pid"` // diagnostics only
	StartedAt       time.Time `json:"startedAt"`
}

// removeLeftovers deletes api.json and the temp files a dead instance left
// behind, and returns what it could not clean up. Only the holder of the lock
// may call it.
func removeLeftovers(dir string) error {
	err := RemoveInfo(dir)
	// ReadDir and a match on the bare name, not Glob on the joined path: the
	// folder's own name may hold pattern characters.
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		err = errors.Join(err, fmt.Errorf("list leftovers: %w", readErr))
	}
	for _, e := range entries {
		if ok, _ := filepath.Match(tempPattern, e.Name()); ok {
			if rmErr := os.Remove(filepath.Join(dir, e.Name())); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove leftover: %w", rmErr))
			}
		}
	}
	return err
}

// WriteInfo writes api.json atomically with owner-only permissions: a temp file
// in the same folder, then a rename over the old file (a stale one included).
func WriteInfo(dir string, info Info) error {
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("encode %s: %w", infoName, err)
	}
	tmp, err := os.CreateTemp(dir, tempPattern) // created 0600
	if err != nil {
		return fmt.Errorf("create temp %s: %w", infoName, err)
	}
	_, err = tmp.Write(append(data, '\n'))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, infoName))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", infoName, err)
	}
	return nil
}

// RemoveInfo deletes api.json; a missing file is not an error.
func RemoveInfo(dir string) error {
	if err := os.Remove(filepath.Join(dir, infoName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", infoName, err)
	}
	return nil
}
