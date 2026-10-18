//go:build linux

// Package proc answers "is this exact process still running?" so a crashed
// owner's leftovers can be cleaned up without ever signaling an unrelated
// process that reused a pid. Identity is the pid plus its start time (clock
// ticks since boot), which a reused pid cannot share.
package proc

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StartTime returns the start time of pid in clock ticks since boot (field 22 of
// /proc/<pid>/stat). ok is false if the process does not exist.
func StartTime(pid int) (int64, bool) {
	if pid <= 0 {
		return 0, false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	// The command name is in parentheses and may contain spaces and parens, so
	// split after the LAST ")".
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(s[i+1:])
	// f[0] is field 3 (state); starttime is field 22 -> f[19].
	if len(f) < 20 {
		return 0, false
	}
	v, err := strconv.ParseInt(f[19], 10, 64)
	return v, err == nil
}

// Alive reports whether pid exists and is the process that started at start.
func Alive(pid int, start int64) bool {
	got, ok := StartTime(pid)
	return ok && got == start && start != 0
}

// KillGroup terminates a process group: TERM, a short grace, then KILL. The
// caller has already verified the group leader's identity with Alive.
func KillGroup(pgid int, grace time.Duration) {
	if pgid <= 1 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pgid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
