//go:build windows

package claude

// startPTY on Windows (ConPTY, job object) comes with #223.
func startPTY(_ string, _, _ []string, _ string) (terminal, error) {
	return nil, unavailable("Claude usage on Windows is not supported yet (#223)")
}
