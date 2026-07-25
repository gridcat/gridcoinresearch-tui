//go:build windows

// Windows half of the post-update restart. Windows refuses to rename over a
// running .exe and has no exec-replace, so both primitives need workarounds the
// Unix build (restart_unix.go) doesn't.
package main

import (
	"os"
	"os/exec"
)

// replaceExecutable swaps newPath onto exe. The key Windows fact: a running
// .exe can't be OVERWRITTEN or DELETED, but it CAN be renamed/moved aside —
// the image loader opens it with FILE_SHARE_DELETE, so MoveFileEx (what
// os.Rename calls) succeeds on the live binary. So we move the running exe to
// ".old", then move the new binary into the freed original path. This is the
// standard Windows self-update sequence (cf. inconshreveable/go-update,
// minio/selfupdate) — no helper process or reboot needed. The ".old" file is
// still the running image and can't be deleted until this process exits; a
// best-effort remove clears any leftover on the next update.
func replaceExecutable(exe, newPath string) error {
	old := exe + ".old"
	_ = os.Remove(old) // clear a leftover from a previous update, if any
	if err := os.Rename(exe, old); err != nil {
		return wrapPerm(err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		// Put the original back so we never leave the user without a binary.
		_ = os.Rename(old, exe)
		return wrapPerm(err)
	}
	_ = os.Remove(old) // will fail while running; harmless, cleared next update
	return nil
}

// restartExec launches a fresh copy of the updated binary and exits the current
// process. Windows has no exec-replace, so a new process sharing the console is
// the closest equivalent; the parent exits immediately to hand the terminal
// over. Only returns if the child fails to start.
func restartExec(path string, args, env []string) error {
	cmd := exec.Command(path, args[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
