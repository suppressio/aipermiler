package codex_test

import "syscall"

func processAlive(pid int) bool {
	const processQueryLimitedInformation = 0x1000
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = syscall.CloseHandle(handle) }()
	var code uint32
	return syscall.GetExitCodeProcess(handle, &code) == nil && code == 259 // STILL_ACTIVE
}
