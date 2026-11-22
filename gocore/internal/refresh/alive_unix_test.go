//go:build !windows

package refresh_test

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// alive: the process exists and is not a zombie waiting to be reaped.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true // no /proc (macOS): kill(0) is the answer
	}
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) == 0 || fields[0] != "Z"
}
