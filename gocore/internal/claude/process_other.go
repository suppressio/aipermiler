//go:build !windows

package claude

import "os/exec"

func configureProcess(_ *exec.Cmd) {}
