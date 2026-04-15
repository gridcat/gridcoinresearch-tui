// Package main is the program entry point. It wires three things together:
//
//   1. LoadConfig  — parses CLI flags, env vars and the optional
//      gridcoinresearch.conf file into a single Config struct.
//   2. NewRPCClient — builds a JSON-RPC client pointed at the daemon.
//   3. NewModel + tea.NewProgram — hands control to Bubble Tea, which owns the
//      terminal from that point on and drives the Model/Update/View loop
//      defined in model.go, update.go and view.go.
package main

import (
	"fmt"
	"os"

	// The tea alias is a Bubble Tea convention. It keeps call sites short
	// (tea.Program, tea.Cmd, tea.Quit, …) without shadowing a real package
	// called "bubbletea" anywhere else in the tree.
	tea "github.com/charmbracelet/bubbletea"
)

// version is overridden at build time via -ldflags "-X main.version=…".
// The Makefile and goreleaser both set this from the git tag.
var version = "dev"

func main() {
	// Handle --version / -v before touching anything else so it works even
	// when the daemon is down or the conf file is broken.
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("gridcoinresearch-tui", version)
		return
	}

	// os.Args[0] is the program name; pass only the real arguments to the
	// config parser.
	cfg, err := LoadConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}

	rpc := NewRPCClient(cfg)
	m := NewModel(cfg, rpc)

	// WithAltScreen puts the terminal into the "alternate screen buffer" so
	// the TUI owns the whole window while it runs and the user's previous
	// shell output reappears intact on exit.
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
}
