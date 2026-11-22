//go:build windows

package service

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isBusy reports whether a rename or delete failed because another process
// holds the file open without FILE_SHARE_DELETE (.NET and PowerShell readers
// by default): replacing it gives access denied, deleting it a sharing violation.
func isBusy(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
