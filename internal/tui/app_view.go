// This file renders the current Model to a string that Bubble Tea writes to
// the terminal. Every frame of the TUI is produced by View() calling one of
// the render* helpers below. Keep in mind:
//
//   - View is a value receiver, it is pure, it cannot mutate state, and
//     Bubble Tea is free to call it as often as it likes.
//
//   - We use lipgloss for styling. A lipgloss.Style is a reusable config:
//     .Foreground(color), .Bold(true), .Width(n), .Border(…), .Padding(…),
//     then .Render(string) to get the final ANSI-coloured text.
//
//   - lipgloss.JoinHorizontal / JoinVertical place already-rendered blocks
//     next to each other, they measure the blocks, align them, and return
//     a new string. No layout engine, just string concatenation with width
//     awareness.
//
//   - All styles that are used on the per-render hot path (every row of
//     the tx list, for example) are defined once at package level so we
//     don't allocate a fresh Style struct on each frame.
package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// View is Bubble Tea's "render a frame" hook. We dispatch to a modal
// renderer if one is open, otherwise fall through to the main dashboard.
// m.width is zero until Bubble Tea delivers the first WindowSizeMsg, so we
// return a placeholder string to avoid dividing by zero in the layout math.
func (m Model) View() string {
	if m.width == 0 {
		return "starting…"
	}
	if m.tooSmall() {
		return m.renderTooSmall()
	}

	switch m.mode {
	case modeSend:
		return m.renderSendModal()
	case modeSign:
		return m.renderSignModal()
	case modeConfig:
		return m.renderConfigModal()
	case modeTxDetail:
		return m.renderTxDetailModal()
	case modeEditLabel:
		return m.renderEditLabelModal()
	case modeAddLabel:
		return m.renderAddLabelModal()
	case modeHelp:
		return m.renderHelpModal()
	case modePolls:
		return m.renderPollsScreen()
	case modePollDetail:
		return m.renderPollDetailModal()
	case modeConsent:
		return m.renderConsentModal()
	case modeUpdate:
		return m.renderUpdateModal()
	}
	return m.renderDashboard()
}

// renderDashboard stacks the five panels of the main screen and does the
// vertical-budget math so nothing gets pushed off screen when the terminal
// is short. Pseudo-layout:
//
//	┌────────────── header ───────────┐
//	│───────────── stats ─────────────│
//	│─────── My Addresses (capped) ───│
//	│─────── Transactions (stretch) ──│
//	│───────────── footer ────────────│
//
// Transactions get priority; addresses are capped to min(available/3, 8).
func (m Model) renderDashboard() string {
	header := m.renderHeader()
	stats := m.renderStats()
	footer := m.renderFooter()

	// Reuse the boxes we just rendered to measure the budget rather than
	// re-rendering them inside availableBodyHeight() every frame.
	available := m.bodyHeight(header, stats, footer)
	addrCap := m.addrPanelHeight(available)
	addrs := m.renderAddresses(addrCap)

	txHeight := available - lipgloss.Height(addrs)
	if txHeight < 3 {
		txHeight = 3
	}
	txs := m.renderTxList(txHeight)

	return lipgloss.JoinVertical(lipgloss.Left, header, stats, addrs, txs, footer)
}

// renderTooSmall replaces every screen while the terminal is under
// minWidth×minHeight. It has to survive any size, down to a single cell, so
// each line is cut to the width and lines past the height are dropped rather
// than left for the terminal to wrap.
func (m Model) renderTooSmall() string {
	lines := []string{
		theme.Warn.Render(ui.Truncate("Terminal too small", m.width)),
		"",
		theme.Muted.Render(ui.Truncate(fmt.Sprintf("needs %d×%d", minWidth, minHeight), m.width)),
		theme.Muted.Render(ui.Truncate(fmt.Sprintf("now %d×%d", m.width, m.height), m.width)),
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		lipgloss.JoinVertical(lipgloss.Center, lines...))
}
