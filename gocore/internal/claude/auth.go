package claude

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// authTimeout bounds `claude auth status` (~0.2 s measured on #183).
const authTimeout = 5 * time.Second

// runAuthStatus runs `claude auth status --json`; nil when it fails, so the probe
// falls back to the PTY markers.
func runAuthStatus(ctx context.Context, bin, dir string, env []string) []byte {
	ctx, cancel := context.WithTimeout(ctx, authTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status", "--json")
	cmd.Dir, cmd.Env = dir, env
	// A helper that inherits stdout would keep Output waiting after the kill.
	cmd.WaitDelay = time.Second
	configureProcess(cmd)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	return out
}

// systemProber runs the real binary.
func systemProber() prober {
	return prober{start: startPTY, auth: runAuthStatus, environ: os.Environ, timing: defaultTiming}
}
