//go:build !windows

package service

// isBusy is always false here: an open file never blocks a rename or delete.
func isBusy(error) bool { return false }
