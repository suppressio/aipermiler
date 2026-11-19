//go:build !windows

package service

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) //nolint:gosec // a file descriptor always fits in an int
}

func isLockHeld(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK)
}
