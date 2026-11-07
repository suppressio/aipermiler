package claude

import (
	"os/exec"
	"syscall"
)

// configureProcess hides the console of a pipe child (`claude auth status`). Never
// for the PTY child: CREATE_NO_WINDOW breaks ConPTY (#183).
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /* CREATE_NO_WINDOW */}
}
