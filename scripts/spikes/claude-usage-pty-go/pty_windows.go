//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	noJob    = flag.Bool("nojob", false, "do not put claude in a kill-on-close job object (leftover check)")
	noWindow = flag.Bool("nowindow", false, "add CREATE_NO_WINDOW to the creation flags")
)

type winSession struct {
	in, out *os.File
	hpc     windows.Handle
	proc    windows.Handle
	job     windows.Handle
	start   time.Time
}

// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION (not in x/sys).
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func start(bin string, args []string, cwd string, env []string) (session, error) {
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		return nil, err
	}
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: cols, Y: rows}, inR, outW, 0, &hpc); err != nil {
		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}
	// conhost holds its own copies now.
	_ = windows.CloseHandle(inR)
	_ = windows.CloseHandle(outW)

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	// The attribute value is the HPCON itself, not a pointer to it (go vet flags this as a possible misuse).
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, unsafe.Pointer(hpc), unsafe.Sizeof(hpc)); err != nil {
		return nil, err
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.Flags = windows.STARTF_USESTDHANDLES // no inherited std handles: the child uses the pseudo console

	cmdline, _ := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{bin}, args...)))
	dir, _ := windows.UTF16PtrFromString(cwd)
	block := envBlock(env)
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if *noWindow {
		flags |= windows.CREATE_NO_WINDOW
	}
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cmdline, nil, nil, false, flags, &block[0], dir, &si.StartupInfo, &pi); err != nil {
		windows.ClosePseudoConsole(hpc)
		return nil, fmt.Errorf("CreateProcess: %w", err)
	}
	s := &winSession{
		in: os.NewFile(uintptr(inW), "pty-in"), out: os.NewFile(uintptr(outR), "pty-out"),
		hpc: hpc, proc: pi.Process, start: time.Now(),
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err == nil {
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		if !*noJob {
			info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		}
		_, _ = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
		_ = windows.AssignProcessToJobObject(job, pi.Process)
		s.job = job
	}
	_, _ = windows.ResumeThread(pi.Thread)
	_ = windows.CloseHandle(pi.Thread)
	return s, nil
}

func envBlock(env []string) []uint16 {
	var b []uint16
	for _, kv := range env {
		if strings.HasPrefix(kv, "=") { // drive-cwd entries like "=C:=C:\\"
			continue
		}
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	return append(b, 0)
}

func (s *winSession) Read(p []byte) (int, error)  { return s.out.Read(p) }
func (s *winSession) Write(p []byte) (int, error) { return s.in.Write(p) }

func (s *winSession) Close() (time.Duration, string) {
	var acc jobAccounting
	_ = windows.QueryInformationJobObject(s.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&acc)), uint32(unsafe.Sizeof(acc)), nil)
	note := fmt.Sprintf("job: %d processes total, %d active before kill", acc.TotalProcesses, acc.ActiveProcesses)
	if *noJob {
		_ = windows.TerminateProcess(s.proc, 1)
		note += "; nojob: TerminateProcess(claude) only"
	} else {
		_ = windows.TerminateJobObject(s.job, 1)
	}
	_, _ = windows.WaitForSingleObject(s.proc, 5000)
	windows.ClosePseudoConsole(s.hpc)
	_ = s.in.Close()
	_ = s.out.Close()
	_ = windows.CloseHandle(s.proc)
	_ = windows.CloseHandle(s.job)
	return time.Duration(acc.TotalUserTime+acc.TotalKernelTime) * 100, note
}
