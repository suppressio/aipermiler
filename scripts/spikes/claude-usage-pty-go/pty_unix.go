//go:build !windows

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

var _ = flag.Bool("nojob", false, "Windows only")
var _ = flag.Bool("nowindow", false, "Windows only")

type unixSession struct {
	f      *os.File
	cmd    *exec.Cmd
	before syscall.Rusage
}

func start(bin string, args []string, cwd string, env []string) (session, error) {
	s := &unixSession{}
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &s.before)
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env = cwd, env
	// pty.Start sets Setsid + Setctty: claude leads its own process group.
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return nil, err
	}
	s.f, s.cmd = f, cmd
	return s, nil
}

func (s *unixSession) Read(p []byte) (int, error)  { return s.f.Read(p) }
func (s *unixSession) Write(p []byte) (int, error) { return s.f.Write(p) }

func (s *unixSession) Close() (time.Duration, string) {
	pid := s.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGKILL) // whole process group
	err := s.cmd.Wait()
	_ = s.f.Close()
	var after syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &after)
	tv := func(t syscall.Timeval) time.Duration { return time.Duration(t.Nano()) }
	cpu := tv(after.Utime) - tv(s.before.Utime) + tv(after.Stime) - tv(s.before.Stime)
	return cpu, fmt.Sprintf("killed pgid %d: %v", pid, err)
}
