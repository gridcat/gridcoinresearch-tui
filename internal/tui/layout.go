package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// bodyHeight is the vertical budget (in rows) the two scrollable panels share:
// the terminal height minus the three fixed boxes. It takes the already-rendered
// boxes so the only caller with them in hand (renderDashboard) doesn't re-render
// to measure.
func (m Model) bodyHeight(header, stats, footer string) int {
	return m.height - lipgloss.Height(header) - lipgloss.Height(stats) - lipgloss.Height(footer)
}

// availableBodyHeight is bodyHeight for callers that don't already have the
// fixed boxes rendered (the +/- resize key handlers), so they render and
// measure on demand. The divider math stays in one place.
func (m Model) availableBodyHeight() int {
	return m.bodyHeight(m.renderHeader(), m.renderStats(), m.renderFooter())
}

// addrPanelHeight is the effective height of the My Addresses panel for the
// given body budget. Without a user override it auto-sizes the same way the
// dashboard always has: a third of the budget, capped at 8 rows so tall
// terminals don't waste space. A non-zero addrPanelRows (set by the resize
// keys) replaces that default. Either way the result is clamped so addresses
// keeps at least 3 rows and Transactions keeps its own 3-row minimum.
func (m Model) addrPanelHeight(available int) int {
	const addrAbsoluteMax = 8
	rows := available / 3
	if rows > addrAbsoluteMax {
		rows = addrAbsoluteMax
	}
	if m.addrPanelRows > 0 {
		rows = m.addrPanelRows
	}
	return m.clampPanelRows(rows, available)
}

// addrPanelMaxRows is the largest height the My Addresses panel may be resized
// to: enough to leave Transactions its 3-row floor (available-3), floored at 3
// itself for very short terminals. A manually-resized panel fills its height
// with blank rows (see renderAddresses), so the ceiling no longer depends on
// how many addresses the active tab happens to show.
func (m Model) addrPanelMaxRows(available int) int {
	max := available - 3
	if max < 3 {
		max = 3
	}
	return max
}

// clampPanelRows pins an address-panel height to [3, addrPanelMaxRows] so
// neither panel drops below its 3-row floor and the resize counter never runs
// past what the panel can actually show.
func (m Model) clampPanelRows(rows, available int) int {
	max := m.addrPanelMaxRows(available)
	if rows < 3 {
		return 3
	}
	if rows > max {
		return max
	}
	return rows
}

// panelRowWidth is the visual column budget for one row in a full-width panel:
// the box's inner text area (m.width-4 for border + padding) minus the
// 2-column row prefix. Clamped to at least 1 so tiny terminals don't produce a
// negative width. Shared by the address and transaction renderers (and the
// address left/right key handler) so their clamps and highlight padding agree.
func (m Model) panelRowWidth() int {
	w := m.width - 6
	if w < 1 {
		w = 1
	}
	return w
}

// addrMaxScroll is the furthest right the address panel can pan: the widest
// row's column count minus the visible row width (never negative). Returns 0
// when every row already fits, which also doubles as "scrolling is pointless"
// for the ←/→ hint. Takes the slice it should measure (the visible tab) so the
// pan range tracks whatever the panel is currently showing.
func (m Model) addrMaxScroll(addrs []rpc.ReceivedAddress, rowWidth int) int {
	widest := 0
	for _, a := range addrs {
		if w := ui.SegmentsWidth(addressRowSegments(a, m.anonymous, m.ownership(a.Address))); w > widest {
			widest = w
		}
	}
	if max := widest - rowWidth; max > 0 {
		return max
	}
	return 0
}

// txListRows returns the number of transaction rows that fit in the current
// dashboard layout. Key handling uses it to preserve the visible transaction
// window between frames.
func (m Model) txListRows() int {
	available := m.availableBodyHeight()
	addrs := m.renderAddresses(m.addrPanelHeight(available))
	txHeight := available - lipgloss.Height(addrs)
	if txHeight < 3 {
		txHeight = 3
	}
	maxRows, _ := ui.ListWindow(txHeight, 0, len(m.txs))
	return maxRows
}

// txWindowOffset keeps the stored transaction window valid for the current
// layout and cursor. Moving up traverses the visible rows before scrolling the
// list once the cursor reaches the top.
func (m Model) txWindowOffset(maxRows int) int {
	maxOffset := len(m.txs) - maxRows
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset := m.txOffset
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if m.txCursor < offset {
		offset = m.txCursor
	}
	if m.txCursor >= offset+maxRows {
		offset = m.txCursor - maxRows + 1
	}
	return offset
}
