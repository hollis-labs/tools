//go:build !linux

package proc

import "time"

// StartTime is unavailable off Linux: identity cannot be verified, so Alive is
// always false and nothing is ever signalled.
func StartTime(int) (int64, bool) { return 0, false }

// Alive is always false off Linux.
func Alive(int, int64) bool { return false }

// KillGroup does nothing off Linux.
func KillGroup(int, time.Duration) {}
