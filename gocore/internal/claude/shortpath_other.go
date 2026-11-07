//go:build !windows

package claude

// shortPath is the identity outside Windows: there are no 8.3 names.
func shortPath(path string) (string, error) { return path, nil }
