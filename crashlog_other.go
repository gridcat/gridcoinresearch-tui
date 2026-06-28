//go:build !linux

package main

import "os"

// redirectStderr is a best-effort fallback on non-linux platforms: reassigning
// os.Stderr captures the program's own writes but NOT the Go runtime's raw
// fd-2 crash dumps (that needs a dup of fd 2, which is platform-specific). The
// crash this guards against was reported on linux/arm64, so the full-fidelity
// path lives in crashlog_linux.go; here we just avoid breaking the build.
func redirectStderr(f *os.File) error {
	os.Stderr = f
	return nil
}
