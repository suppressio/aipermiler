//go:build windows

package refresh

import (
	"os/exec"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processTree puts the child in a job object that kills every process in it
// when closed. A cancel terminates the job; after the child ends the job is
// closed, so nothing it started outlives the read. A process the child starts
// before it is assigned to the job (right after the start) escapes it: the CLI
// starts its providers only after parsing its flags.
type processTree struct {
	cmd *exec.Cmd
	job atomic.Uintptr // windows.Handle; read by the cancel, which runs on exec's goroutine
}

func newProcessTree(cmd *exec.Cmd) *processTree {
	// No console window at every read (Electron passes windowsHide). The
	// window is only hidden (SW_HIDE): whether CREATE_NO_WINDOW is safe for a
	// child that hosts a ConPTY session (Claude) is not verified here.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	t := &processTree{cmd: cmd}
	cmd.Cancel = func() error {
		if job := windows.Handle(t.job.Load()); job != 0 {
			return windows.TerminateJobObject(job, 1)
		}
		return cmd.Process.Kill()
	}
	return t
}

func (t *processTree) started() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(t.cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	t.job.Store(uintptr(job))
}

func (t *processTree) sweep() {
	if job := windows.Handle(t.job.Swap(0)); job != 0 {
		_ = windows.CloseHandle(job)
	}
}
