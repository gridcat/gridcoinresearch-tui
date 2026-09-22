// Command gridcoinresearch-tui is a full-screen terminal dashboard for a
// running gridcoinresearchd wallet daemon.
//
// Everything here is startup wiring that has to happen before the TUI can
// take the terminal: the --version short-circuit, config resolution, the
// masked password prompt and the --debug-log stderr redirect. The dashboard
// itself lives in internal/tui.
package main

import (
	"fmt"
	"os"

	// term gives us IsTerminal + ReadPassword for the masked startup
	// prompt when a user is configured without a password. Already pulled
	// in transitively by Bubble Tea, so no extra module cost.
	"github.com/charmbracelet/x/term"

	"github.com/gridcat/gridcoinresearch-tui/internal/buildinfo"
	"github.com/gridcat/gridcoinresearch-tui/internal/config"
	"github.com/gridcat/gridcoinresearch-tui/internal/selfupdate"
	"github.com/gridcat/gridcoinresearch-tui/internal/tui"
)

// debugLogFile holds the open --debug-log file for the whole process lifetime
// so it isn't garbage-collected (and closed) after main wires up the stderr
// redirect. nil when --debug-log was not given.
var debugLogFile *os.File

func main() {
	// Handle --version / -v before touching anything else so it works even
	// when the daemon is down or the conf file is broken.
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("gridcoinresearch-tui", buildinfo.Version)
		return
	}

	// os.Args[0] is the program name; pass only the real arguments to the
	// config parser.
	cfg, err := config.LoadConfig(os.Args[1:])
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
			fmt.Fprintln(os.Stderr, "config: --rpc-user is set but no password was found in --rpc-password, GRC_RPC_PASSWORD or the conf file, and stdin is not a terminal, so refusing to prompt")
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
			if err := tui.RedirectStderr(f); err != nil {
				fmt.Fprintln(os.Stderr, "debug-log: redirect failed:", err)
			}
		}
	}

	restartExe, err := tui.Run(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}

	// If the user ran a self-update, the final model carried the path of the
	// freshly-installed binary. Bubble Tea has now restored the terminal, so
	// it's safe to re-exec into the new version and the user lands right back in
	// the running app. On Unix RestartExec replaces this process and never
	// returns; on Windows it spawns + exits.
	if restartExe != "" {
		if err := selfupdate.RestartExec(restartExe, os.Args, os.Environ()); err != nil {
			fmt.Fprintln(os.Stderr, "restart:", err)
			os.Exit(1)
		}
	}
}
