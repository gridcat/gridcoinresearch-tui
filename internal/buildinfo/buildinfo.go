// Package buildinfo carries the single fact that has to be injected at link
// time: which version of the program this binary is.
//
// It lives in its own package because four otherwise-independent packages need
// it (the update check, the telemetry payload, the telemetry User-Agent and
// the UI), and a variable in package main is not reachable from any of them.
package buildinfo

// Version is overridden at build time via
//
//	-ldflags "-X github.com/gridcat/gridcoinresearch-tui/internal/buildinfo.Version=…"
//
// The Makefile and goreleaser both set it from the git tag. A build that
// forgets the flag is not an error; it just reports itself as a dev build,
// which is what a local `go build` should say.
var Version = "dev"
