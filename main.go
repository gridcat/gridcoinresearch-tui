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

	// term gives us IsTerminal + ReadPassword for the masked startup
	// prompt when a user is configured without a password. Already pulled
	// in transitively by Bubble Tea, so no extra module cost.
	"github.com/charmbracelet/x/term"
)

// version is overridden at build time via -ldflags "-X main.version=…".
// The Makefile and goreleaser both set this from the git tag.
var version = "dev"

// debugLogFile holds the open --debug-log file for the whole process lifetime
// so it isn't garbage-collected (and closed) after main wires up the stderr
// redirect. nil when --debug-log was not given.
var debugLogFile *os.File

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

	// If a username resolved but no password did, prompt for it before the
	// TUI takes over. Skipped when stdin isn't a TTY so headless runs (CI,
	// docker exec without -t, piped stdin) fail fast with a clear message
	// instead of blocking forever on Read.
	if cfg.User != "" && cfg.Password == "" {
		if !term.IsTerminal(os.Stdin.Fd()) {
			fmt.Fprintln(os.Stderr, "config: --rpc-user is set but no password was found in --rpc-password, GRC_RPC_PASSWORD or the conf file, and stdin is not a terminal — refusing to prompt")
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "RPC password for %s: ", cfg.User)
		pw, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Fprintln(os.Stderr) // ReadPassword consumes the trailing newline; print our own
		if err != nil {
			fmt.Fprintln(os.Stderr, "config: failed to read password:", err)
			os.Exit(2)
		}
		cfg.Password = string(pw)
	}

	// If --debug-log is set, point stderr at the file before the TUI takes
	// over, so a Go runtime crash dump (which writes to fd 2 and skips Bubble
	// Tea's terminal restore) is captured to the file instead of corrupting the
	// alt-screen display. Done after the password prompt so that prompt still
	// reaches the terminal. Best-effort: a failure here must not stop the TUI.
	if cfg.DebugLog != "" {
		if f, err := os.OpenFile(cfg.DebugLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "debug-log:", err)
		} else {
			debugLogFile = f // keep the file alive for the process lifetime
			if err := redirectStderr(f); err != nil {
				fmt.Fprintln(os.Stderr, "debug-log: redirect failed:", err)
			}
		}
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
