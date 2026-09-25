package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// renderHelpModal is the read-only cheat sheet opened with "?". It lists every
// key grouped by what it does, with a short plain-language note on each, plus a
// one-line summary of what the dashboard is. Any key closes it (handled in
// app_keys.go::handleKey, case modeHelp).
func (m Model) renderHelpModal() string {
	// keyRow renders one "keys → what they do" line: the keys in the accent
	// colour in a fixed-width column so the descriptions line up.
	keyRow := func(keys, desc string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			theme.Accent.Width(12).Render(keys),
			theme.Label.Render(desc),
		)
	}

	lines := []string{
		theme.Muted.Render("A read-only view of a running Gridcoin wallet: balance, staking,"),
		theme.Muted.Render("lock, block height, your addresses, and recent transactions."),
		theme.Muted.Render("You can also send coins, sign a message, or manage address labels."),
		"",
		theme.Title.Render("Move around"),
		keyRow("↑ ↓  k j", "Move the cursor in the focused panel"),
		keyRow("PgUp PgDn", "Jump a page (also Ctrl+U / Ctrl+D)"),
		keyRow("g G", "First / last row (also Home / End)"),
		keyRow("Tab", "Switch focus between the two panels"),
		keyRow("← →  h l", "Slide a too-wide row sideways"),
		"",
		theme.Title.Render("My Addresses"),
		keyRow("1 2 3", "Show Mine, Others, or All addresses"),
		keyRow("/", "Search labels or addresses; Enter keeps, Esc clears"),
		keyRow("+ −", "Grow or shrink the panel; 0 resets it"),
		keyRow("e", "Rename the selected address (blank clears it)"),
		keyRow("n", "Add a labeled address-book entry"),
		"",
		theme.Title.Render("Do things"),
		keyRow("Enter", "Open the selected transaction in full"),
		keyRow("s", "Send GRC (choose a saved label or type an address)"),
		keyRow("m", "Sign a message with one of your addresses"),
		keyRow("p", "Browse on-chain governance polls (tab: all / active)"),
		keyRow("c", "Change host, port, login or refresh for this session,"),
		keyRow("", "and turn peer sharing on or off (that one is remembered)"),
		keyRow("u", "Check GitHub for a newer release and update in place"),
		keyRow("a", "Hide every amount on screen, handy when sharing"),
		keyRow("r", "Refresh now instead of waiting for the next poll"),
		keyRow("? q", "This help; q quits (also Ctrl+C)"),
		"",
		theme.Muted.Render("press any key to close"),
	}

	modalWidth := 66
	if max := m.width - 4; modalWidth > max && max > 0 {
		modalWidth = max
	}
	modal := ui.ModalBox(modalWidth, "Help", lipgloss.JoinVertical(lipgloss.Left, lines...))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// handleHelpKey drives the read-only help sheet: any key dismisses it.
func (m Model) handleHelpKey(_ tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.mode = modeDashboard
	return m, nil
}
