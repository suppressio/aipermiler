//go:build windows

package claude

import (
	"errors"
	"os"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// conTerminal is claude.exe in a pseudo console (ConPTY), inside a job object that
// holds its whole process tree (#183, docs/SPIKE-CLAUDE-USAGE-PTY.md).
type conTerminal struct {
	in, out *os.File
	hpc     windows.Handle
	proc    windows.Handle
	job     windows.Handle
	exited  chan struct{}
	close   sync.Once
}

// startPTY starts bin directly (a native .exe, never a .cmd/.ps1 shim) in a new
// pseudo console. It is created suspended, put in a kill-on-close job, then resumed,
// so no helper can start outside the job. Never CREATE_NO_WINDOW: it breaks ConPTY,
// and the pseudo console shows no window anyway (verified on #183).
func startPTY(bin string, args, env []string, dir string) (terminal, error) {
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		closeHandles(inR, inW)
		return nil, err
	}
	var hpc windows.Handle
	err := windows.CreatePseudoConsole(windows.Coord{X: ptyCols, Y: ptyRows}, inR, outW, 0, &hpc)
	// The pseudo console holds its own copies of these ends.
	closeHandles(inR, outW)
	if err != nil {
		closeHandles(inW, outR)
		return nil, err
	}
	t := &conTerminal{in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out"), hpc: hpc, exited: make(chan struct{})}
	if err := t.spawn(bin, args, env, dir); err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = t.in.Close()
		_ = t.out.Close()
		return nil, err
	}
	go t.watch()
	return t, nil
}

func (t *conTerminal) spawn(bin string, args, env []string, dir string) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	// The attribute value is the HPCON itself, not a pointer to it: its bits are
	// passed as the pointer (read through &hpc, so no uintptr becomes a pointer).
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&t.hpc)), unsafe.Sizeof(t.hpc)); err != nil {
		return err
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	// No inherited standard handles: the child uses the pseudo console.
	si.Flags = windows.STARTF_USESTDHANDLES
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{bin}, args...)))
	if err != nil {
		return err
	}
	cwd, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	block, err := envBlock(env)
	if err != nil {
		return err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		closeHandles(job)
		return err
	}
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cmdline, nil, nil, false, flags, &block[0], cwd, &si.StartupInfo, &pi); err != nil {
		closeHandles(job)
		return err
	}
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		closeHandles(pi.Thread, pi.Process, job)
		return err
	}
	_, err = windows.ResumeThread(pi.Thread)
	closeHandles(pi.Thread)
	if err != nil {
		_ = windows.TerminateJobObject(job, 1)
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		closeHandles(pi.Process, job)
		return err
	}
	t.proc, t.job = pi.Process, job
	return nil
}

// watch closes the pseudo console once claude.exe ended: only then does the output
// pipe reach EOF, which is how the driver sees an exit.
func (t *conTerminal) watch() {
	_, _ = windows.WaitForSingleObject(t.proc, windows.INFINITE)
	t.close.Do(func() { windows.ClosePseudoConsole(t.hpc) })
	close(t.exited)
}

// envBlock is the UTF-16 environment block, without the per-drive cwd entries.
func envBlock(env []string) ([]uint16, error) {
	var block []uint16
	for _, kv := range env {
		if strings.HasPrefix(kv, "=") {
			continue
		}
		if strings.ContainsRune(kv, 0) {
			return nil, errors.New("environment entry holds a NUL")
		}
		block = append(block, utf16.Encode([]rune(kv))...)
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func closeHandles(handles ...windows.Handle) {
	for _, h := range handles {
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
	}
}

func (t *conTerminal) Read(p []byte) (int, error)  { return t.out.Read(p) }
func (t *conTerminal) Write(p []byte) (int, error) { return t.in.Write(p) }

// Kill ends every process of the job, helpers included.
func (t *conTerminal) Kill() error { return windows.TerminateJobObject(t.job, 1) }

// Wait returns once claude.exe ended and the pseudo console is closed.
func (t *conTerminal) Wait() error {
	<-t.exited
	_ = t.in.Close()
	_ = t.out.Close()
	closeHandles(t.proc, t.job)
	return nil
}
