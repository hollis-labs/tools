package main

import "golang.org/x/sys/unix"

// RENAME_NOREPLACE makes the must-not-exist rule atomic, including a destination
// created after preflight. Staging and output are siblings on the same volume.
func publishDirectory(stage, output string) error {
	return unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, output, unix.RENAME_NOREPLACE)
}
