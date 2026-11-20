// Package service is the always-on part of the core: the process that stays up
// (`aipermiler serve`), its single-instance lock, the loopback HTTP API and the
// api.json file clients read to find it (#247, approach in #246).
package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// ErrAlreadyRunning means another process holds the service lock.
var ErrAlreadyRunning = errors.New("service already running")

const lockName = "service.lock"

// Lock is the single-instance lock. The operating system drops it when the
// process dies, however it dies, so a crash never leaves a stale lock.
type Lock struct {
	f atomic.Pointer[os.File] // swapped out by Release: safe to call twice, from any goroutine
}

// AcquireLock takes the exclusive lock in dir without waiting. It returns
// ErrAlreadyRunning when another process (or another Lock) holds it.
func AcquireLock(dir string) (*Lock, error) {
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // dir is the core's own config folder
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if isLockHeld(err) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock %s: %w", lockName, err)
	}
	l := &Lock{}
	l.f.Store(f)
	return l, nil
}

// Release gives the lock up. The file stays: it carries no data.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	f := l.f.Swap(nil)
	if f == nil {
		return nil
	}
	if err := f.Close(); err != nil { // closing the descriptor releases the lock
		return fmt.Errorf("release lock: %w", err)
	}
	return nil
}
