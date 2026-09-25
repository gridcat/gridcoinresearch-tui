package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
	"github.com/mattn/go-runewidth"
)

func fetchAddrs(c *rpc.Client) tea.Cmd {
	return func() tea.Msg {
		a, err := c.ListAddressBook()
		return addrsMsg{a, err}
	}
}

// fetchAddrOwnership resolves the ownership of each given address via
// validateaddress, serially so the TUI never holds more than one daemon RPC
// worker at a time (the same good-neighbour policy as refreshAllCmd). Callers
// pass only addresses whose ownership isn't cached yet, so on an idle wallet
// this fires once per genuinely new address and then stays quiet.
//
// We need it because listreceivedbyaddress returns the whole address book,
// including foreign addresses you've merely labelled, and those carry no
// involvesWatchonly flag, so they're otherwise indistinguishable from your
// own. validateaddress.ismine is (IsMine != ISMINE_NO): true for spendable
// and watch-only addresses, false only for foreign ones, the same test the
// official Qt wallet uses to split its Receiving and Sending address lists.
func fetchAddrOwnership(c *rpc.Client, addrs []string) tea.Cmd {
	return func() tea.Msg {
		mine := make(map[string]bool, len(addrs))
		for _, a := range addrs {
			v, err := c.ValidateAddress(a)
			if err != nil {
				continue // leave unresolved; retried on the next address refresh
			}
			mine[a] = v.IsMine
		}
		return addrMineMsg{mine}
	}
}

// renderAddrTabs builds the Mine | Others | All tab bar shown as the panel's
// header line. Each segment is prefixed with its 1/2/3 hotkey so the binding is
// self-documenting (e.g. "1.Mine 12"). The active tab is bracketed and
// accented; inactive tabs are muted and padded with spaces so the bar's width
// doesn't jump when the selection moves. Counts come from addrTabCounts.
// compact drops the parentheses and the search hint so the bar fits a
// 42-column terminal; "/" is still listed under "?".
func (m Model) renderAddrTabs(compact bool) string {
	mine, others, all := m.addrTabCounts()
	seg := func(tab addrTab, key, label string, n int) string {
		text := fmt.Sprintf("%s.%s (%d)", key, label, n)
		if compact {
			text = fmt.Sprintf("%s %s %d", key, label, n)
		}
		if m.addrTab == tab {
			return theme.Accent.Render("[" + text + "]")
		}
		return theme.Muted.Render(" " + text + " ")
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Top,
		seg(addrTabMine, "1", "Mine", mine), " ",
		seg(addrTabOthers, "2", "Others", others), " ",
		seg(addrTabAll, "3", "All", all),
	)
	if compact {
		return tabs
	}
	return tabs + theme.Muted.Render("  [/] search")
}

// renderAddresses draws the scrollable My Addresses panel. Like
// renderTxList, it derives the visible window from the cursor each
// frame. The panel renders a focus indicator (accent border + ▸ on the
// selected row) only when m.focusedArea == focusAddr. Rows are drawn from the
// active tab's slice (see visibleAddresses), with the tab bar as the header
// and an inline search row while a query is being edited or applied.
//
// compact is the narrow-terminal dashboard, where this panel takes the whole
// body when it is the active tab: it fills maxHeight, and the position
// counter and ←/→ hint move from the tab bar into the bottom border.
func (m Model) renderAddresses(maxHeight int, compact bool) string {
	border := theme.Border
	if m.focusedArea == focusAddr {
		border = theme.BorderFocused
	}
	box := border.Width(m.width - 2)
	// Once the user has manually resized the panel, hold that height and pad
	// with blank rows (same as the Transactions box) so it stays a consistent
	// size when switching tabs instead of snapping to each tab's row count. In
	// auto mode we leave the box content-sized so a near-empty panel yields its
	// slack to Transactions.
	if m.addrPanelRows > 0 || compact {
		box = box.Height(maxHeight - 2)
	}

	titleStyle := theme.Title
	if m.focusedArea == focusAddr {
		titleStyle = theme.Accent
	}
	footer := ""
	titled := func(content string) string {
		return ui.TitledBoxFooter(box, titleStyle, "My Addresses", footer, content)
	}
	if !m.addrsLoaded {
		return titled(theme.Muted.Render("loading…"))
	}
	if m.addrsErr != "" {
		return titled(theme.Bad.Render("error: " + format.SanitizeTerminal(m.addrsErr)))
	}
	if len(m.addresses) == 0 {
		return titled(theme.Muted.Render("wallet has no addresses yet, run `getnewaddress`"))
	}

	// The tab bar is always rendered, even when the active tab is empty, so the
	// user can switch away from a tab that filtered everything out.
	visible := m.visibleAddresses()
	query := strings.TrimSpace(m.addrSearch.Value())
	showSearch := m.addrSearch.Focused() || query != ""
	searchRow := ""
	if showSearch {
		// Keep the input within the panel even on narrow terminals. At roomy
		// widths the row also carries the relevant keys; the help screen remains
		// the fallback when there is only space for the match count.
		matchWord := "matches"
		if len(visible) == 1 {
			matchWord = "match"
		}
		suffix := fmt.Sprintf("  %d %s", len(visible), matchWord)
		if m.width >= 76 {
			if m.addrSearch.Focused() {
				suffix += " · enter keep · esc clear"
			} else {
				suffix += " · / edit · esc clear"
			}
		}
		searchInput := m.addrSearch
		searchInput.Width = m.panelRowWidth() - runewidth.StringWidth("Search: ") - runewidth.StringWidth(suffix)
		if searchInput.Width > 36 {
			searchInput.Width = 36
		}
		if searchInput.Width < 4 {
			searchInput.Width = 4
		}
		searchRow = theme.Accent.Render("Search: ") + searchInput.View() + theme.Muted.Render(suffix)
	}
	if len(visible) == 0 {
		empty := "no addresses in this tab"
		if query != "" {
			empty = "no labels or addresses match"
		}
		lines := []string{m.renderAddrTabs(compact)}
		if showSearch {
			lines = append(lines, searchRow)
		}
		lines = append(lines, theme.Muted.Render(empty))
		return titled(strings.Join(lines, "\n"))
	}

	// Available data rows inside the box: borders + tab bar, plus the optional
	// search row. The filter therefore never makes the panel exceed its budget.
	headerRows := 1
	if showSearch {
		headerRows++
	}
	maxRows := maxHeight - 2 - headerRows
	if maxRows < 1 {
		maxRows = 1
	}

	// Derive the window offset from the cursor, same pattern as renderTxList.
	offset := 0
	if m.addrCursor >= maxRows {
		offset = m.addrCursor - maxRows + 1
	}

	rowWidth := m.panelRowWidth()
	// maxScroll walks the visible rows, so compute it once and reuse it for both
	// the clamp and the ←/→ hint below.
	maxScroll := m.addrMaxScroll(visible, rowWidth)
	// Clamp the horizontal scroll so it can't pan past the longest row.
	hoff := m.addrHScroll
	if hoff > maxScroll {
		hoff = maxScroll
	}

	// Header: tab bar, plus a "cursor/total" indicator when the list is longer
	// than the window, and a ←/→ hint once a row is wide enough to scroll and
	// the panel is focused.
	header := m.renderAddrTabs(compact)
	counter, arrows := "", ""
	if len(visible) > maxRows {
		counter = fmt.Sprintf("%d/%d", m.addrCursor+1, len(visible))
	}
	if m.focusedArea == focusAddr && maxScroll > 0 {
		arrows = "←/→"
	}
	if compact {
		footer = strings.TrimSpace(arrows + " " + counter)
	} else {
		if counter != "" {
			header += theme.Muted.Render("  " + counter)
		}
		if arrows != "" {
			header += theme.Muted.Render("  " + arrows)
		}
	}
	lines := []string{header}
	if showSearch {
		lines = append(lines, searchRow)
	}

	end := offset + maxRows
	if end > len(visible) {
		end = len(visible)
	}
	for i := offset; i < end; i++ {
		prefix := "  "
		row := ui.ClipSegments(addressRowSegments(visible[i], m.anonymous, m.ownership(visible[i].Address)), hoff, rowWidth)
		if i == m.addrCursor && m.focusedArea == focusAddr {
			// Carry the highlight background through the cursor marker and
			// across the whole row, so it's coloured edge to edge.
			prefix = theme.Accent.Background(theme.ColorRowSelected).Render("▸ ")
			row = ui.FillBackground(row, rowWidth)
		}
		lines = append(lines, prefix+row)
	}
	return titled(strings.Join(lines, "\n"))
}

// addressRowSegments builds the coloured runs for one address: the address
// itself, then optional watch-only flag, label, and received amount, each
// separated by a two-space gap.
func addressRowSegments(a rpc.ReceivedAddress, anonymous bool, own addrOwnership) []ui.Seg {
	// Sanitize the daemon-sourced texts as the segments are built: every
	// consumer (addrMaxScroll's widest-row measurement, clipSegments'
	// column slicing) measures these exact strings, so cleaning them any
	// later would make the horizontal-scroll math disagree with what is
	// actually printed.
	segs := []ui.Seg{{Text: format.SanitizeTerminal(a.Address), Style: theme.Value}}
	gap := ui.Seg{Text: "  ", Style: theme.Muted}
	if own == ownForeign {
		// listreceivedbyaddress includes addresses you've only labelled but
		// don't own; flag them in red (a stronger cue than watch-only's
		// orange) so a foreign address is never mistaken for one of yours.
		segs = append(segs, gap, ui.Seg{Text: "⚠ not yours", Style: theme.Bad})
	}
	if a.InvolvesWatchonly {
		// The eye glyph hints at the meaning visually; the trailing word
		// makes it explicit on terminals that fall back to a tofu box.
		// styleWarn (orange) is the same shade used for "wallet locked"
		// in the stats panel, both convey "this needs attention before
		// you try to sign or spend".
		segs = append(segs, gap, ui.Seg{Text: "👁 watch-only", Style: theme.Warn})
	}
	if l := format.SanitizeTerminal(a.DisplayLabel()); l != "" {
		segs = append(segs, gap, ui.Seg{Text: l, Style: theme.Muted})
	}
	if a.Amount > 0 {
		amt := "received " + format.FormatGRCPlain(a.Amount)
		if anonymous {
			amt = "received " + format.MaskedAmount
		}
		segs = append(segs, gap, ui.Seg{Text: amt, Style: theme.Good})
	}
	return segs
}

// addressLabel returns the wallet address-book label for address. Transaction
// records have no label of their own, while the independently refreshed
// addresses slice is the daemon's authoritative address-book view.
func (m Model) addressLabel(address string) string {
	if address == "" {
		return ""
	}
	for _, known := range m.addresses {
		if known.Address == address {
			return known.DisplayLabel()
		}
	}
	return ""
}

// onAddrsMsg handles addrsMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onAddrsMsg(msg addrsMsg) (tea.Model, tea.Cmd) {
	m.finishFetch()
	if msg.err != nil {
		m.addrsErr = msg.err.Error()
		m.addrsLoaded = true
	} else {
		m.addresses = msg.a
		m.addrsLoaded = true
		m.addrsErr = ""
		// Mirror the tx-list clamp so the cursor never points past
		// the end when the daemon returns a shorter list than before.
		// Clamp against the active tab's length, since that's what the
		// cursor indexes into.
		m.addrCursor = clampCursor(m.addrCursor, len(m.visibleAddresses()))
		// Resolve ownership for any address we haven't validated yet so
		// the panel can flag foreign (not-yours) entries.
		if unknown := m.unknownOwnership(); len(unknown) > 0 {
			spin := m.bumpInflight(1)
			return m, tea.Batch(fetchAddrOwnership(m.rpc, unknown), spin)
		}
	}
	return m, nil
}

// onAddrMineMsg handles addrMineMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onAddrMineMsg(msg addrMineMsg) (tea.Model, tea.Cmd) {
	m.finishFetch()
	for a, mine := range msg.mine {
		m.addrMine[a] = mine
	}
	// Resolving ownership can move a row out of the active tab (e.g. an
	// unknown row turns out foreign and leaves the Mine view), so re-clamp
	// the cursor against the now-current visible length.
	m.addrCursor = clampCursor(m.addrCursor, len(m.visibleAddresses()))
	return m, nil
}

// addrTab identifies which ownership filter the My Addresses panel is showing.
// The user switches between them with the 1/2/3 keys. The zero value is
// addrTabMine, so the panel defaults to the user's own addresses.
type addrTab int

const (
	addrTabMine   addrTab = iota // own + not-yet-resolved addresses (default)
	addrTabOthers                // foreign addresses (labelled send targets)
	addrTabAll                   // the full address book
)

// addrOwnership is the resolved ownership of an address shown in the My
// Addresses panel. listreceivedbyaddress returns the whole address book, so a
// labelled foreign address (a send target) appears there looking just like
// one of your own, the danger the issue tracker flags. validateaddress tells
// the two apart; until it has, we say nothing rather than imply ownership.
type addrOwnership int

const (
	ownUnknown addrOwnership = iota // validateaddress hasn't resolved it yet
	ownMine                         // validateaddress reported ismine
	ownForeign                      // validateaddress reported NOT ismine
)

// ownership reports whether addr is one of the wallet's own addresses,
// reading the cache populated by fetchAddrOwnership.
func (m Model) ownership(addr string) addrOwnership {
	mine, ok := m.addrMine[addr]
	switch {
	case !ok:
		return ownUnknown
	case mine:
		return ownMine
	default:
		return ownForeign
	}
}

// unknownOwnership returns the currently shown addresses whose ownership
// hasn't been resolved yet, so callers can validate just those.
func (m Model) unknownOwnership() []string {
	var out []string
	for _, a := range m.addresses {
		if _, ok := m.addrMine[a.Address]; !ok {
			out = append(out, a.Address)
		}
	}
	return out
}

// visibleAddresses returns the addresses the panel should show under the active
// tab. The partition is gap-free (Mine ∪ Others = All): Mine keeps everything
// that isn't known-foreign (owned, or not yet resolved), so freshly loaded rows
// appear immediately and only drop out if validateaddress later flags them
// foreign. Others keeps exactly the foreign ones. All returns the full slice
// untouched. The cursor, scroll, sign, and edit paths all read this, so the
// filter lives in one place. A search query further narrows the active tab by
// label or address as the user types. Labels match case-insensitively; address
// matching preserves case because Gridcoin addresses themselves are
// case-sensitive.
func (m Model) visibleAddresses() []rpc.ReceivedAddress {
	query := strings.TrimSpace(m.addrSearch.Value())
	if m.addrTab == addrTabAll && query == "" {
		return m.addresses
	}
	labelQuery := strings.ToLower(query)
	wantForeign := m.addrTab == addrTabOthers // else addrTabMine: keep non-foreign
	var out []rpc.ReceivedAddress
	for _, a := range m.addresses {
		if m.addrTab != addrTabAll && (m.ownership(a.Address) == ownForeign) != wantForeign {
			continue
		}
		if query != "" &&
			!strings.Contains(strings.ToLower(a.DisplayLabel()), labelQuery) &&
			!strings.Contains(a.Address, query) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// selectedAddress returns the address highlighted in the My Addresses panel
// (the addrCursor row of the active tab), or nil when the tab is empty or the
// cursor is somehow out of range. Centralizing the cursor-into-filtered-list
// bounds check here means callers (the edit/sign entry points) don't each
// re-derive visibleAddresses() and re-apply the same guard.
func (m Model) selectedAddress() *rpc.ReceivedAddress {
	visible := m.visibleAddresses()
	if m.addrCursor < 0 || m.addrCursor >= len(visible) {
		return nil
	}
	return &visible[m.addrCursor]
}

// addrTabCounts returns the per-tab entry counts for the tab bar. others counts
// the known-foreign addresses; mine is everything else (own + unknown), which
// matches the visibleAddresses partition.
func (m Model) addrTabCounts() (mine, others, all int) {
	all = len(m.addresses)
	for _, a := range m.addresses {
		if m.ownership(a.Address) == ownForeign {
			others++
		}
	}
	mine = all - others
	return
}
