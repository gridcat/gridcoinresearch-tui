package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
)

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeSend:
		return m.handleSendKey(msg)
	case modeSign:
		return m.handleSignKey(msg)
	case modeConfig:
		return m.handleConfigKey(msg)
	case modeTxDetail:
		return m.handleTxDetailKey(msg)
	case modeEditLabel:
		return m.handleEditLabelKey(msg)
	case modeAddLabel:
		return m.handleAddLabelKey(msg)
	case modeHelp:
		return m.handleHelpKey(msg)
	case modePolls:
		return m.handlePollsKey(msg)
	case modePollDetail:
		return m.handlePollDetailKey(msg)
	case modeConsent:
		return m.handleConsentKey(msg)
	case modeUpdate:
		return m.handleUpdateKey(msg)
	}

	// While the inline address search has focus, all printable keys belong to
	// its text input. Enter keeps the current filter and returns to list
	// navigation; Esc clears it. Resetting the cursor on every edit makes the
	// first matching row immediately visible and keeps selections in bounds.
	if m.addrSearch.Focused() {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			m.addrSearch.Blur()
			return m, nil
		case "esc":
			m.addrSearch.SetValue("")
			m.addrSearch.Blur()
			m.addrCursor = 0
			m.addrHScroll = 0
			return m, nil
		}
		before := m.addrSearch.Value()
		var cmd tea.Cmd
		m.addrSearch, cmd = m.addrSearch.Update(msg)
		if m.addrSearch.Value() != before {
			m.addrCursor = 0
			m.addrHScroll = 0
		}
		return m, cmd
	}
	// Dashboard-mode keys.
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		// Anti-pileup: if a previous refresh is still running, ignore
		// the keystroke instead of stacking another 6-fetch sequence
		// behind it. The spinner already tells the user a refresh is
		// in progress.
		if m.inflight > 0 {
			return m, nil
		}
		spin := m.bumpInflight(6)
		return m, tea.Batch(m.refreshAllCmd(), spin)
	case "s":
		m.openSendModal()
		return m, nil
	case "m":
		m.openSignModal()
		return m, nil
	case "e":
		// Edit the label of the highlighted address. Only meaningful when the
		// addresses panel is focused and has a valid selection (selectedAddress
		// returns nil for an empty tab or out-of-range cursor).
		if m.focusedArea == focusAddr && m.selectedAddress() != nil {
			m.openEditLabelModal()
		}
		return m, nil
	case "n":
		m.openAddLabelModal()
		return m, nil
	case "c":
		m.openConfigModal()
		return m, nil
	case "u":
		// Open the Updates modal and always kick off a fresh check. This is
		// also the manual "check now", so pressing u never shows a stale result.
		m.mode = modeUpdate
		m.update.step = updateStepChecking
		m.update.errMsg = ""
		m.update.missedReleases = nil
		return m, checkUpdateCmd(true)
	case "p":
		// Open the full-screen polls list and (re)load it. Lazy on purpose:
		// polls are never fetched on the refresh tick.
		m.mode = modePolls
		return m, m.reloadPolls()
	case "?":
		m.mode = modeHelp
		return m, nil
	case "a":
		m.anonymous = !m.anonymous
		return m, nil
	case "/":
		// Search is useful regardless of which panel currently has focus, so the
		// shortcut also moves focus to Addresses. Slash mirrors terminal tools
		// such as less and Vim, and tmux passes it through without a prefix
		// conflict. An existing query is retained and made ready for editing.
		m.focusedArea = focusAddr
		m.addrCursor = 0
		m.addrHScroll = 0
		m.addrSearch.CursorEnd()
		return m, m.addrSearch.Focus()
	case "esc":
		// After Enter has committed a filter, Esc is the quick way back to the
		// full active tab. With no filter it remains the dashboard's usual no-op.
		if m.addrSearch.Value() != "" {
			m.addrSearch.SetValue("")
			m.addrCursor = 0
			m.addrHScroll = 0
		}
		return m, nil
	case "tab":
		// Toggle the arrow-key focus between the tx list and the addresses panel.
		if m.focusedArea == focusTx {
			m.focusedArea = focusAddr
		} else {
			m.focusedArea = focusTx
		}
		// Start each visit to the address panel from the left edge.
		m.addrHScroll = 0
		return m, nil
	case "left", "h":
		if m.focusedArea == focusAddr && m.addrHScroll > 0 {
			m.addrHScroll--
		}
		return m, nil
	case "right", "l":
		if m.focusedArea == focusAddr && m.addrHScroll < m.addrMaxScroll(m.visibleAddresses(), m.panelRowWidth()) {
			m.addrHScroll++
		}
		return m, nil
	case "1", "2", "3":
		// Switch the addresses tab (Mine / Others / All). Reset the cursor and
		// horizontal pan so each tab starts at the top-left, since the visible
		// rows differ. The key digit maps directly onto the addrTab enum.
		m.addrTab = addrTab(msg.String()[0] - '1')
		m.addrCursor = 0
		m.addrHScroll = 0
		return m, nil
	case "enter":
		// Enter only opens the tx detail modal; pressing it while the
		// addresses panel is focused is a no-op on purpose.
		if m.focusedArea == focusTx && len(m.txs) > 0 && m.txCursor >= 0 && m.txCursor < len(m.txs) {
			m.mode = modeTxDetail
			// Last-resort contract lookup for the row the user is actually
			// looking at. Normally the type is already cached from the
			// txsMsg batch, but that batch drops txids whose lookup failed,
			// so this doubles as the retry path: opening the modal asks
			// again, and it fills itself in when the reply lands. If this
			// attempt fails too the field keeps saying "resolving…" until
			// the next wallet change or the next time the modal is opened.
			tx := m.txs[m.txCursor]
			if _, ok := m.txContracts[tx.TxID]; !ok && format.IsContractCandidate(tx) {
				spin := m.bumpInflight(1)
				return m, tea.Batch(fetchContracts(m.rpc, []string{tx.TxID}), spin)
			}
		}
		return m, nil
	case "up", "k":
		m.scrollBy(-1)
		return m, nil
	case "down", "j":
		m.scrollBy(1)
		return m, nil
	case "pgup", "ctrl+u":
		m.scrollBy(-pageSize)
		return m, nil
	case "pgdown", "ctrl+d":
		m.scrollBy(pageSize)
		return m, nil
	case "+", "=":
		// Grow the addresses panel (push the divider down). Seed from the
		// current effective height so the first press never jumps, and clamp
		// so Transactions keeps its 3-row minimum.
		available := m.availableBodyHeight()
		m.addrPanelRows = m.clampPanelRows(m.addrPanelHeight(available)+1, available)
		return m, nil
	case "-":
		// Shrink the addresses panel (push the divider up), floored at 3 rows.
		available := m.availableBodyHeight()
		m.addrPanelRows = m.clampPanelRows(m.addrPanelHeight(available)-1, available)
		return m, nil
	case "0":
		// Snap the split back to the auto-computed default.
		m.addrPanelRows = 0
		return m, nil
	case "g", "home":
		m.scrollTo(0)
		return m, nil
	case "G", "end":
		_, length := m.focusedList()
		m.scrollTo(length - 1)
		return m, nil
	}
	return m, nil
}

// pageSize is the fixed step used by pgup/pgdn/ctrl+u/ctrl+d. Keeping it
// constant (rather than computing it from the panel's visible height) means
// the scroll speed is predictable regardless of which panel is focused or
// how tall the terminal currently is.
const pageSize = 10

// focusedList returns a pointer to the cursor field of the currently-
// focused scrollable list and the length of its backing slice. Every
// scroll helper goes through this accessor so the focusedArea dispatch
// lives in exactly one place, so adding a third panel later only needs a
// new case here, not in every helper that scrolls.
func (m *Model) focusedList() (*int, int) {
	if m.focusedArea == focusAddr {
		// Scroll within the active tab, not the full book.
		return &m.addrCursor, len(m.visibleAddresses())
	}
	return &m.txCursor, len(m.txs)
}

// scrollBy moves the cursor of the currently-focused list by delta rows,
// clamped to [0, len-1]. Positive delta scrolls down.
func (m *Model) scrollBy(delta int) {
	cursor, length := m.focusedList()
	*cursor = clampCursor(*cursor+delta, length)
	if m.focusedArea == focusTx {
		m.txOffset = m.txWindowOffset(m.txListRows())
	}
}

// scrollTo jumps the cursor of the currently-focused list to an absolute
// position. Negative values clamp to 0 and values past the end clamp to
// the last row, so the caller is free to pass length-1 for "go to end".
func (m *Model) scrollTo(pos int) {
	cursor, length := m.focusedList()
	*cursor = clampCursor(pos, length)
	if m.focusedArea == focusTx {
		m.txOffset = m.txWindowOffset(m.txListRows())
	}
}

// clampCursor pins a desired cursor position to the valid range
// [0, length-1]. An empty list always clamps to 0.
func clampCursor(c, length int) int {
	if length == 0 {
		return 0
	}
	if c < 0 {
		return 0
	}
	if c >= length {
		return length - 1
	}
	return c
}

// focusArea identifies which scrollable list on the dashboard is "active",
// i.e. which one arrow keys / page keys / enter apply to. The user
// toggles between them with the tab key.
type focusArea int

const (
	focusTx   focusArea = iota // transactions panel (default)
	focusAddr                  // My Addresses panel
)
