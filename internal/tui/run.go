// Package tui is the wallet dashboard itself: the Bubble Tea Model, its
// Update/View loop and every screen, panel and modal that loop drives.
//
// It is a normal package rather than package main so that the command in
// cmd/gridcoinresearch-tui stays thin wiring, and so the app can be
// imported, which the plugin-host work in docs/plugins-plan.md needs, since
// the wallet there becomes a host driven by the SDK's engine.
//
// The three pieces of the Elm architecture live in:
//
//   - model.go      holds the Model struct with all state
//   - app_update.go Update: a message in, (Model, Cmd) out
//   - app_view.go   View: the Model rendered to a string
//
// Both dispatchers are deliberately thin; each case delegates to the feature
// that owns it (panel_*.go, screen_*.go, modal_*.go).
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gridcat/gridcoinresearch-tui/internal/config"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
)

// Run paints the chrome, builds the client and the Model, and hands the
// terminal to Bubble Tea until the user quits.
//
// It returns the path of a freshly-installed binary when the user ran a
// self-update, and "" otherwise. Re-execing into it is the caller's job: that
// has to happen after Bubble Tea has restored the terminal, which it does as
// p.Run returns, and the caller is the one that still holds os.Args.
func Run(cfg config.Config) (restartExe string, err error) {
	// termenv v0.15.2 does not recognise plain TERM=xterm on Unix. Apply a
	// conservative ANSI fallback before anything renders, while preserving
	// explicit color opt-outs and every stronger profile it already detected.
	theme.ConfigureColorProfile()

	// Paint the chrome for the resolved network before the first frame, so a
	// testnet window is recognisable at a glance among mainnet ones.
	theme.ApplyNetwork(cfg.Testnet)

	client := rpc.New(rpc.Options{URL: cfg.URL(), User: cfg.User, Password: cfg.Password})

	// WithAltScreen puts the terminal into the "alternate screen buffer" so
	// the TUI owns the whole window while it runs and the user's previous
	// shell output reappears intact on exit.
	p := tea.NewProgram(NewModel(cfg, client), tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return "", err
	}
	if fm, ok := finalModel.(Model); ok {
		return fm.restartExe, nil
	}
	return "", nil
}
