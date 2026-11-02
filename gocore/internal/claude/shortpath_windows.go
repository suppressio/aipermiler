//go:build windows

package claude

import "golang.org/x/sys/windows"

// shortPath returns the 8.3 short form of an existing path (no spaces), or the
// path itself when the volume keeps no short names.
func shortPath(path string) (string, error) {
	long, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil {
		return "", err
	}
	if int(n) > len(buf) {
		return "", windows.ERROR_INSUFFICIENT_BUFFER
	}
	return windows.UTF16ToString(buf[:n]), nil
}
