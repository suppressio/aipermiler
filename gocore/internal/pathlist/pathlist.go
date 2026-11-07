// Package pathlist splits a PATH value for a given OS, so binary discovery can be
// tested for every OS on one host (filepath.SplitList follows the host OS).
package pathlist

import "strings"

// Split splits PATH for goos. On Windows an entry may be quoted ("D:\Tools\Codex")
// and may then contain the separator; the quotes are removed, as cmd.exe and
// filepath.SplitList on Windows do.
func Split(value, goos string) []string {
	if value == "" {
		return nil
	}
	if goos != "windows" {
		return strings.Split(value, ":")
	}
	var entries []string
	var current strings.Builder
	quoted := false
	for _, r := range value {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ';' && !quoted:
			entries = append(entries, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	return append(entries, current.String())
}
