//go:build !windows

package claude

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

type ptyTerminal struct {
	file *os.File
	cmd  *exec.Cmd
}

// startPTY starts bin in a new PTY. pty.Start sets Setsid and Setctty, so claude
// leads its own process group and Kill reaches its helpers too.
func startPTY(bin string, args, env []string, dir string) (terminal, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env = dir, env
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: ptyRows, Cols: ptyCols})
	if err != nil {
		return nil, err
	}
	return &ptyTerminal{file, cmd}, nil
}

func (t *ptyTerminal) Read(p []byte) (int, error)  { return t.file.Read(p) }
func (t *ptyTerminal) Write(p []byte) (int, error) { return t.file.Write(p) }

// Kill ends the whole process group.
func (t *ptyTerminal) Kill() error { return syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL) }

func (t *ptyTerminal) Wait() error {
	err := t.cmd.Wait()
	_ = t.file.Close()
	return err
}
