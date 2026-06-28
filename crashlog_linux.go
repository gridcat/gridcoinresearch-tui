//go:build linux

package main

import (
	"os"
	"syscall"
)

// redirectStderr points the process's standard error (fd 2) at f. The Go
// runtime writes fatal-error crash dumps (out-of-memory, concurrent map
// access, etc.) straight to fd 2 and these bypass every deferred cleanup,
// including Bubble Tea's terminal restore — so without this they land on the
// alt-screen terminal, get lost, and leave the terminal in raw mode. Dup'ing
// fd 2 onto the log file captures them instead.
//
// Dup3 (not Dup2) is used because Dup2 isn't defined on linux/arm64 — the very
// platform issue #4 was reported on — while Dup3 is available on every linux
// arch.
func redirectStderr(f *os.File) error {
	return syscall.Dup3(int(f.Fd()), int(os.Stderr.Fd()), 0)
}
