//go:build !windows

// Unix half of the post-update restart. Two OS-specific primitives live here:
// replacing the on-disk binary, and re-executing the process. Both are trivial
// on Unix and fiddly on Windows (see restart_windows.go).
package main

import (
	"os"
	"syscall"
)

// replaceExecutable atomically moves newPath onto exe. On Unix a running
// binary's file can be replaced by rename while the process still runs — the
// kernel keeps the old inode open for the live process — so this is a single
// atomic step with no move-aside dance.
func replaceExecutable(exe, newPath string) error {
	if err := os.Rename(newPath, exe); err != nil {
		return wrapPerm(err)
	}
	return nil
}

// restartExec replaces the current process image with a fresh exec of path,
// keeping the same PID and inheriting the terminal. It only returns on failure;
// on success execution never comes back here.
func restartExec(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}
