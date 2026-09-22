package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// renderStatusBar renders one bordered full-width bar: the key legend on the
// left, and the refresh spinner pinned to the right while any RPC fetch is in
// flight. Shared by the dashboard footer and the polls-screen footer so their
// width/gap budget can't drift.
func (m Model) renderStatusBar(keys []string) string {
	left := theme.Muted.Render(strings.Join(keys, "  "))
	right := ""
	// Peer sharing reports its result here and nowhere else. It is a
	// background courtesy to the network, so a failure is worth showing but
	// never worth a modal or an error colour that implies the wallet is
	// broken.
	if m.sharingNote != "" {
		right = theme.Muted.Render(format.SanitizeTerminal(m.sharingNote)) + "  "
	}
	if m.inflight > 0 {
		right += theme.Accent.Render(ui.SpinnerFrames[m.spinnerFrame])
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if gap < 1 {
		gap = 1
	}
	line := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
	return theme.Border.Width(m.width - 2).Render(line)
}

// renderPollsFooter is the key legend for the polls screen: scope toggle,
// scrolling, refresh, and back.
func (m Model) renderPollsFooter() string {
	scopeKey := "[tab] show active"
	if !m.pollsShowFinished {
		scopeKey = "[tab] show all"
	}
	return m.renderStatusBar([]string{
		scopeKey,
		"[enter] details",
		"[↑/↓ · pgup/pgdn] scroll",
		"[r]efresh",
		"[esc] back",
	})
}

func (m Model) renderFooter() string {
	anonLabel := "[a]non"
	if m.anonymous {
		anonLabel = "[a]non ●"
	}
	keys := []string{"[?]help", "[s]end", "sign [m]sg", "[n]ew label"}
	// [e]dit label only acts on the focused addresses panel, so surface it
	// contextually rather than implying it works everywhere. (The 1/2/3 tab
	// keys are self-documented in the panel's own tab bar.)
	if m.focusedArea == focusAddr {
		keys = append(keys, "[/] search addresses", "[e]dit label")
	}
	keys = append(keys,
		"[p]olls",
		"[c]onfig",
		"[u]pdate",
		"[r]efresh",
		anonLabel,
		"[tab] switch panel",
		"[↑/↓ · pgup/pgdn] scroll",
		"[+/-] resize",
		"[q]uit",
	)
	// The right side shows a spinning Braille dot while any RPC fetch is in
	// flight so the user can see the TUI is alive and talking to the daemon;
	// when all fetches settle it goes blank, a brief flash every refresh
	// interval rather than a persistent clock.
	return m.renderStatusBar(keys)
}
