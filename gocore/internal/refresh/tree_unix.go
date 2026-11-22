//go:build !windows

package refresh

import (
	"os/exec"
	"syscall"
)

// processTree puts the child in its own process group. A cancel sends SIGTERM
// to the group, so the CLI cancels its read and ends its own children (the
// Claude PTY runs in a session of its own and is ended by the CLI); after the
// child is reaped, whatever is left in the group is killed.
type processTree struct{ cmd *exec.Cmd }

func newProcessTree(cmd *exec.Cmd) *processTree {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	return &processTree{cmd: cmd}
}

func (t *processTree) started() {}

func (t *processTree) sweep() {
	if t.cmd.Process != nil {
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
	}
}
