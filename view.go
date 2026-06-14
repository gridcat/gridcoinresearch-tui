// This file renders the current Model to a string that Bubble Tea writes to
// the terminal. Every frame of the TUI is produced by View() calling one of
// the render* helpers below. Keep in mind:
//
//   • View is a value receiver, it is pure, it cannot mutate state, and
//     Bubble Tea is free to call it as often as it likes.
//
//   • We use lipgloss for styling. A lipgloss.Style is a reusable config:
//     .Foreground(color), .Bold(true), .Width(n), .Border(…), .Padding(…),
//     then .Render(string) to get the final ANSI-coloured text.
//
//   • lipgloss.JoinHorizontal / JoinVertical place already-rendered blocks
//     next to each other, they measure the blocks, align them, and return
//     a new string. No layout engine, just string concatenation with width
//     awareness.
//
//   • All styles that are used on the per-render hot path (every row of
//     the tx list, for example) are defined once at package level so we
//     don't allocate a fresh Style struct on each frame.
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Colour palette. lipgloss.Color accepts any 256-colour terminal code as a
// decimal string, and the terminal renders it via ANSI SGR. Where the
// terminal doesn't support colour, lipgloss strips the escape sequences.
var (
	colorBorder  = lipgloss.Color("240")
	colorMuted   = lipgloss.Color("244")
	colorLabel   = lipgloss.Color("250")
	colorValue   = lipgloss.Color("255")
	colorGood    = lipgloss.Color("42")  // green
	colorWarn    = lipgloss.Color("214") // orange
	colorBad     = lipgloss.Color("203") // red
	colorMainnet = lipgloss.Color("42")
	colorTestnet = lipgloss.Color("214")
	colorAccent  = lipgloss.Color("75")

	// styleBorder is the rounded-corner box used for every panel on the
	// dashboard. Padding(0, 1) inserts one column of horizontal breathing
	// room inside the border on each side.
	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	styleLabel  = lipgloss.NewStyle().Foreground(colorLabel)
	styleValue  = lipgloss.NewStyle().Foreground(colorValue).Bold(true)
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleGood   = lipgloss.NewStyle().Foreground(colorGood)
	styleWarn   = lipgloss.NewStyle().Foreground(colorWarn)
	styleBad    = lipgloss.NewStyle().Foreground(colorBad)
	styleAccent = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleTitle  = lipgloss.NewStyle().Foreground(colorValue).Bold(true)

	styleRowSelected = lipgloss.NewStyle().
				Background(lipgloss.Color("236")).
				Foreground(colorValue)

	// styleBorderFocused is the same rounded box but painted with the
	// accent colour so the user can tell at a glance which panel arrow
	// keys will operate on.
	styleBorderFocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorAccent).
				Padding(0, 1)

	styleMainnetBadge = lipgloss.NewStyle().Foreground(colorMainnet).Bold(true)
	styleTestnetBadge = lipgloss.NewStyle().Foreground(colorTestnet).Bold(true)

	styleStatLabelA = styleLabel.Width(14)
	styleStatLabelB = styleLabel.Width(12)
	styleStatValueA = lipgloss.NewStyle().Width(22)

	styleTxStatusCol = lipgloss.NewStyle().Width(10).Foreground(colorLabel)
	styleTxAmountCol = lipgloss.NewStyle().Width(18).Align(lipgloss.Right)
	styleTxAddrCol   = lipgloss.NewStyle().Width(16)
	styleTxTimeCol   = lipgloss.NewStyle().Width(12)
)

// txKindStyle maps the status enum defined in format.go to the lipgloss
// colour we want its icon rendered in. Package-level map so renderTxRow
// doesn't build one on each frame.
var txKindStyle = map[TxStatusKind]lipgloss.Style{
	TxStatusUpcoming:  styleWarn,
	TxStatusIncoming:  styleAccent,
	TxStatusSending:   styleAccent,
	TxStatusConfirmed: styleGood,
	TxStatusStake:     styleAccent,
}

var (
	configLabelStyle   = styleLabel.Width(12)
	configLabelFocused = styleAccent.Width(12)
	configValueFocused = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
)

// View is Bubble Tea's "render a frame" hook. We dispatch to a modal
// renderer if one is open, otherwise fall through to the main dashboard.
// m.width is zero until Bubble Tea delivers the first WindowSizeMsg, so we
// return a placeholder string to avoid dividing by zero in the layout math.
func (m Model) View() string {
	if m.width == 0 {
		return "starting…"
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
	}
	return m.renderDashboard()
}

// renderDashboard stacks the five panels of the main screen and does the
// vertical-budget math so nothing gets pushed off screen when the terminal
// is short. Pseudo-layout:
//
//     ┌────────────── header ──────────────┐
//     │───────────── stats ─────────────│
//     │─────── My Addresses (capped) ───│
//     │─────── Transactions (stretch) ──│
//     │───────────── footer ────────────│
//
// Transactions get priority; addresses are capped to min(available/3, 8).
func (m Model) renderDashboard() string {
	header := m.renderHeader()
	stats := m.renderStats()
	footer := m.renderFooter()

	// Transactions are the primary working area: they get the lion's share
	// of the vertical budget. Addresses are a reference panel, capped at a
	// third of what's left after the fixed rows, with an absolute ceiling so
	// very tall terminals don't waste space on them either.
	available := m.height - lipgloss.Height(header) - lipgloss.Height(stats) - lipgloss.Height(footer)
	const addrAbsoluteMax = 8
	addrCap := available / 3
	if addrCap > addrAbsoluteMax {
		addrCap = addrAbsoluteMax
	}
	if addrCap < 3 {
		addrCap = 3
	}
	addrs := m.renderAddresses(addrCap)

	txHeight := available - lipgloss.Height(addrs)
	if txHeight < 3 {
		txHeight = 3
	}
	txs := m.renderTxList(txHeight)

	return lipgloss.JoinVertical(lipgloss.Left, header, stats, addrs, txs, footer)
}

// renderHeader draws the top bar: program name on the left, network badge
// in the middle, current block height right-aligned. We measure the two
// rendered halves with lipgloss.Width and pad the gap with spaces so the
// right half lands at the right edge of the box.
func (m Model) renderHeader() string {
	networkBadge := styleMainnetBadge.Render("● mainnet")
	if m.cfg.Testnet {
		networkBadge = styleTestnetBadge.Render("● testnet")
	}
	if m.chain.Chain == "test" && !m.cfg.Testnet {
		networkBadge = styleBad.Render("✗ daemon is TESTNET, TUI is mainnet")
	} else if m.chain.Chain == "main" && m.cfg.Testnet {
		networkBadge = styleBad.Render("✗ daemon is MAINNET, TUI is testnet")
	}

	title := styleTitle.Render("gridcoinresearch-tui")
	blockInfo := ""
	if m.chain.Blocks > 0 {
		blockInfo = styleMuted.Render("block " + groupThousandsInt64(m.chain.Blocks))
	}

	leftHalf := lipgloss.JoinHorizontal(lipgloss.Top, title, "  ", networkBadge)
	gap := m.width - lipgloss.Width(leftHalf) - lipgloss.Width(blockInfo) - 4
	if gap < 1 {
		gap = 1
	}
	line := lipgloss.JoinHorizontal(lipgloss.Top,
		leftHalf,
		strings.Repeat(" ", gap),
		blockInfo,
	)

	return styleBorder.Width(m.width - 2).Render(line)
}

// unconfirmedReceived totals coins received but not yet confirmed enough to
// count toward Balance — the rows shown as "upcoming"/"incoming" in the tx
// grid. We derive it from the tx list because getwalletinfo.unconfirmed_balance
// does NOT include funds received from other people: it reads 0 for them while
// the Qt wallet's own GetUnconfirmedBalance() counts them, so trusting that
// field alone leaves freshly received coins invisible up top. Adding this back
// in mirrors the Qt overview, where these land in "Unconfirmed" and Total but
// not in Available/Balance.
func (m Model) unconfirmedReceived() float64 {
	var total float64
	for _, tx := range m.txs {
		if tx.Amount <= 0 {
			continue
		}
		switch ClassifyTransaction(tx).Kind {
		case TxStatusUpcoming, TxStatusIncoming:
			total += tx.Amount
		}
	}
	return total
}

func (m Model) renderStats() string {
	if !m.loaded {
		return styleBorder.Width(m.width - 2).Render(styleMuted.Render("loading wallet…"))
	}

	fmtBal := func(v float64) string {
		if m.anonymous {
			return styleValue.Render(MaskedAmount)
		}
		return styleValue.Render(FormatGRCPlain(v))
	}

	// The daemon's unconfirmed_balance omits funds received from others, so add
	// those back from the tx list (see unconfirmedReceived). Safe to sum: the
	// field only ever carries our own trusted pending (e.g. change from a send
	// we made), a disjoint set from the received-pending we derive.
	unconfirmed := m.wallet.UnconfirmedBalance + m.unconfirmedReceived()

	balanceRow := statRow("Balance", fmtBal(m.wallet.Balance),
		"Staking", m.stakingBadge())
	unconfRow := statRow("Unconfirmed", fmtBal(unconfirmed),
		"Wallet", m.lockBadge())
	immatureRow := statRow("Immature", fmtBal(m.wallet.ImmatureBalance),
		"Difficulty", styleValue.Render(fmt.Sprintf("%.4f", m.staking.Difficulty.Value())))

	rows := []string{balanceRow, unconfRow, immatureRow}
	// A maturing stake shows up in getwalletinfo's stake/newmint, not in
	// immature_balance: that field only counts coinbase outputs, which a
	// pure PoS chain never has, so it stays 0. Without this row the locked
	// coins are invisible. They've left balance but appear nowhere else.
	// Skip it when there's no stake so idle wallets don't show a bare 0.00.
	//
	// We read stake and ignore newmint, since in Gridcoin both run the same
	// code (credit from immature coinstakes), so the two are always equal, and
	// newmint is just a leftover Peercoin name. One line matches the Qt
	// wallet's "Immature Stake".
	if m.wallet.Stake != 0 {
		rows = append(rows, statRow("Immature Stake", fmtBal(m.wallet.Stake), "", ""))
	}

	// Total mirrors the Qt wallet's overview: everything the wallet holds,
	// spendable or not. Same sum the GUI uses (balance + stake + unconfirmed
	// + immature), so the figure lines up 1:1 with what people see there.
	total := m.wallet.Balance + m.wallet.Stake + unconfirmed + m.wallet.ImmatureBalance
	rows = append(rows, statRow("Total", fmtBal(total), "", ""))

	content := lipgloss.JoinVertical(lipgloss.Left, rows...)
	if m.walletErr != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, content, "", styleBad.Render("error: "+m.walletErr))
	}
	return styleBorder.Width(m.width - 2).Render(content)
}

func statRow(labelA, valueA, labelB, valueB string) string {
	left := lipgloss.JoinHorizontal(lipgloss.Top,
		styleStatLabelA.Render(labelA),
		styleStatValueA.Render(valueA),
	)
	right := lipgloss.JoinHorizontal(lipgloss.Top,
		styleStatLabelB.Render(labelB),
		valueB,
	)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
}

func (m Model) stakingBadge() string {
	if m.staking.Staking {
		label := "● yes"
		if eta := FormatStakeETA(m.staking.ExpectedTime); eta != "—" {
			label += " (~" + eta + ")"
		}
		return styleGood.Render(label)
	}
	if m.staking.MiningError != "" {
		return styleWarn.Render("○ " + m.staking.MiningError)
	}
	return styleMuted.Render("○ no")
}

func (m Model) lockBadge() string {
	if m.wallet.UnlockedUntil == nil {
		return styleMuted.Render("● unencrypted")
	}
	if m.wallet.IsLocked() {
		return styleWarn.Render("● locked")
	}
	remaining := time.Until(time.Unix(*m.wallet.UnlockedUntil, 0))
	return styleGood.Render("● unlocked " + FormatDuration(remaining))
}

// addrRowWidth is the visual column budget for one address row: the box's
// inner text area (m.width-4 for border + padding) minus the 2-column row
// prefix. Clamped to at least 1 so tiny terminals don't produce a negative
// width. Shared by the renderer and the left/right key handler so their
// scroll clamps agree.
func (m Model) addrRowWidth() int {
	w := m.width - 6
	if w < 1 {
		w = 1
	}
	return w
}

// addrMaxScroll is the furthest right the address panel can pan: the widest
// row's column count minus the visible row width (never negative). Returns 0
// when every row already fits, which also doubles as "scrolling is pointless"
// for the ←/→ hint.
func (m Model) addrMaxScroll(rowWidth int) int {
	widest := 0
	for _, a := range m.addresses {
		if w := segmentsWidth(addressRowSegments(a, m.anonymous)); w > widest {
			widest = w
		}
	}
	if max := widest - rowWidth; max > 0 {
		return max
	}
	return 0
}

// renderAddresses draws the scrollable My Addresses panel. Like
// renderTxList, it derives the visible window from the cursor each
// frame. The panel renders a focus indicator (accent border + ▸ on the
// selected row) only when m.focusedArea == focusAddr.
func (m Model) renderAddresses(maxHeight int) string {
	border := styleBorder
	if m.focusedArea == focusAddr {
		border = styleBorderFocused
	}
	box := border.Width(m.width - 2)

	title := "My Addresses"
	if !m.addrsLoaded {
		return box.Render(styleTitle.Render(title) + "\n" + styleMuted.Render("loading…"))
	}
	if m.addrsErr != "" {
		return box.Render(styleTitle.Render(title) + "\n" + styleBad.Render("error: "+m.addrsErr))
	}
	if len(m.addresses) == 0 {
		return box.Render(styleTitle.Render(title) + "\n" + styleMuted.Render("wallet has no addresses yet, run `getnewaddress`"))
	}

	// Available data rows inside the box: maxHeight - 2 (borders) - 1 (title row).
	maxRows := maxHeight - 3
	if maxRows < 1 {
		maxRows = 1
	}

	// Derive the window offset from the cursor, same pattern as renderTxList.
	offset := 0
	if m.addrCursor >= maxRows {
		offset = m.addrCursor - maxRows + 1
	}

	rowWidth := m.addrRowWidth()
	// Clamp the horizontal scroll so it can't pan past the longest row.
	hoff := m.addrHScroll
	if max := m.addrMaxScroll(rowWidth); hoff > max {
		hoff = max
	}

	// Title carries the total count plus a "cursor/total" indicator when
	// the list is longer than the window, and a ←/→ hint once a row is wide
	// enough to scroll and the panel is focused.
	header := fmt.Sprintf("My Addresses (%d)", len(m.addresses))
	if len(m.addresses) > maxRows {
		header += fmt.Sprintf("  %d/%d", m.addrCursor+1, len(m.addresses))
	}
	if m.focusedArea == focusAddr && m.addrMaxScroll(rowWidth) > 0 {
		header += styleMuted.Render("  ←/→")
	}
	lines := []string{styleTitle.Render(header)}

	end := offset + maxRows
	if end > len(m.addresses) {
		end = len(m.addresses)
	}
	for i := offset; i < end; i++ {
		prefix := "  "
		row := clipSegments(addressRowSegments(m.addresses[i], m.anonymous), hoff, rowWidth)
		if i == m.addrCursor && m.focusedArea == focusAddr {
			prefix = styleAccent.Render("▸ ")
			row = styleRowSelected.Render(row)
		}
		lines = append(lines, prefix+row)
	}
	return box.Render(strings.Join(lines, "\n"))
}

// styledSeg is one coloured run of an address row. We keep rows as a list of
// (plain text, style) pairs rather than a single pre-rendered string so the
// horizontal-scroll window can slice them by visual column and still style
// each visible piece — slicing an already-rendered ANSI string by column is
// what the pinned x/ansi can't do (it only truncates from the right).
type styledSeg struct {
	text  string
	style lipgloss.Style
}

// addressRowSegments builds the coloured runs for one address: the address
// itself, then optional watch-only flag, label, and received amount, each
// separated by a two-space gap.
func addressRowSegments(a ReceivedAddress, anonymous bool) []styledSeg {
	segs := []styledSeg{{a.Address, styleValue}}
	gap := styledSeg{"  ", styleMuted}
	if a.InvolvesWatchonly {
		// The eye glyph hints at the meaning visually; the trailing word
		// makes it explicit on terminals that fall back to a tofu box.
		// styleWarn (orange) is the same shade used for "wallet locked"
		// in the stats panel, both convey "this needs attention before
		// you try to sign or spend".
		segs = append(segs, gap, styledSeg{"👁 watch-only", styleWarn})
	}
	if l := a.DisplayLabel(); l != "" {
		segs = append(segs, gap, styledSeg{l, styleMuted})
	}
	if a.Amount > 0 {
		amt := "received " + FormatGRCPlain(a.Amount)
		if anonymous {
			amt = "received " + MaskedAmount
		}
		segs = append(segs, gap, styledSeg{amt, styleGood})
	}
	return segs
}

// segmentsWidth is the total visual column count of a row, used to clamp the
// horizontal scroll so it can't pan past the longest line.
func segmentsWidth(segs []styledSeg) int {
	w := 0
	for _, s := range segs {
		w += runewidth.StringWidth(s.text)
	}
	return w
}

// clipSegments renders the row through a horizontal window [offset, offset+
// width) of visual columns, styling each visible slice. A muted ‹ marks
// content hidden off the left edge and › content hidden off the right; each
// marker reserves one column so the result never exceeds width (and so never
// wraps to a second line).
func clipSegments(segs []styledSeg, offset, width int) string {
	if width < 1 {
		return ""
	}
	total := segmentsWidth(segs)

	avail := width
	left := ""
	if offset > 0 {
		left = styleMuted.Render("‹")
		avail--
	}
	right := ""
	if total-offset > avail {
		right = styleMuted.Render("›")
		avail--
	}
	if avail < 0 {
		avail = 0
	}

	end := offset + avail
	var b strings.Builder
	b.WriteString(left)
	col := 0
	for _, seg := range segs {
		segStart := col
		segEnd := col + runewidth.StringWidth(seg.text)
		col = segEnd
		if segEnd <= offset || segStart >= end {
			continue
		}
		lo := offset
		if segStart > lo {
			lo = segStart
		}
		hi := end
		if segEnd < hi {
			hi = segEnd
		}
		b.WriteString(seg.style.Render(sliceByCols(seg.text, lo-segStart, hi-segStart)))
	}
	b.WriteString(right)
	return b.String()
}

// sliceByCols returns the substring of text covering visual columns [lo, hi).
// A wide glyph that would straddle either boundary is dropped whole rather
// than split; zero-width runes (combining marks, variation selectors) stay
// attached to the base glyph they follow.
func sliceByCols(text string, lo, hi int) string {
	if hi <= lo {
		return ""
	}
	var b strings.Builder
	col := 0
	for _, r := range text {
		w := runewidth.RuneWidth(r)
		if w == 0 {
			if col > lo && col <= hi {
				b.WriteRune(r)
			}
			continue
		}
		if col >= hi {
			break
		}
		if col >= lo && col+w <= hi {
			b.WriteRune(r)
		}
		col += w
	}
	return b.String()
}


// renderTxList draws the scrollable transactions panel, sized to fill the
// vertical space that renderDashboard handed it. Scroll math:
//
//   txCursor: index of the currently selected tx in m.txs
//   offset: index of the tx shown at the top of the visible window
//               (derived fresh every frame from cursor + maxRows)
//   maxRows: how many data rows fit inside the box this frame
//
// We slide offset just enough to keep the cursor in view.
func (m Model) renderTxList(height int) string {
	border := styleBorder
	if m.focusedArea == focusTx {
		border = styleBorderFocused
	}
	boxStyle := border.Width(m.width - 2).Height(height - 2)
	title := styleTitle.Render("Transactions")
	if !m.txsLoaded {
		return boxStyle.Render(title + "\n" + styleMuted.Render("loading…"))
	}
	if m.txsErr != "" {
		return boxStyle.Render(title + "\n" + styleBad.Render("error: "+m.txsErr))
	}
	if len(m.txs) == 0 {
		return boxStyle.Render(title + "\n" + styleMuted.Render("no transactions yet"))
	}

	// Rows that actually show tx data: subtract borders (2), title (1).
	maxRows := height - 3
	if maxRows < 1 {
		maxRows = 1
	}
	if maxRows > len(m.txs) {
		maxRows = len(m.txs)
	}

	// Derive the window offset from the cursor. Starts from 0 and slides
	// forward when the cursor moves past the current window.
	offset := 0
	if m.txCursor >= maxRows {
		offset = m.txCursor - maxRows + 1
	}

	lines := []string{title}
	for i := offset; i < offset+maxRows && i < len(m.txs); i++ {
		prefix := "  "
		line := renderTxRow(m.txs[i], m.anonymous)
		if i == m.txCursor && m.focusedArea == focusTx {
			// Highlight only the focused panel's cursor row. An unfocused
			// tx list leaves the cursor as a silent bookmark, symmetric
			// with the addresses panel so the two behave the same.
			prefix = styleAccent.Render("▸ ")
			line = styleRowSelected.Render(line)
		}
		lines = append(lines, prefix+line)
	}
	return boxStyle.Render(strings.Join(lines, "\n"))
}

func renderTxRow(tx Transaction, anonymous bool) string {
	st := ClassifyTransaction(tx)
	iconStyle, ok := txKindStyle[st.Kind]
	if !ok {
		iconStyle = styleMuted
	}
	icon := iconStyle.Render(st.Icon)
	status := styleTxStatusCol.Render(st.Label)

	var amountCol string
	if anonymous {
		amountCol = styleTxAmountCol.Render(styleMuted.Render(MaskedAmount))
	} else {
		amountStyle := styleValue
		switch {
		case tx.Amount < 0:
			amountStyle = styleWarn
		case tx.Amount > 0:
			amountStyle = styleGood
		}
		amountCol = styleTxAmountCol.Render(amountStyle.Render(FormatGRC(tx.Amount)))
	}

	addr := tx.Address
	if addr == "" && (tx.Category == "generate" || tx.Category == "immature") {
		addr = "(stake)"
	}
	addrCol := styleTxAddrCol.Render(ShortAddress(addr))

	timeCol := styleTxTimeCol.Render(styleMuted.Render(FormatRelativeTime(tx.Time)))
	catCol := styleMuted.Render(tx.Category)

	return lipgloss.JoinHorizontal(lipgloss.Top,
		icon, " ", status, amountCol, "  ", addrCol, "  ", timeCol, "  ", catCol,
	)
}

func (m Model) renderFooter() string {
	anonLabel := "[a]non"
	if m.anonymous {
		anonLabel = "[a]non ●"
	}
	keys := []string{"[s]end", "sign [m]sg"}
	// [e]dit label only acts on the focused addresses panel, so surface it
	// contextually rather than implying it works everywhere.
	if m.focusedArea == focusAddr {
		keys = append(keys, "[e]dit label")
	}
	keys = append(keys,
		"[c]onfig",
		"[r]efresh",
		anonLabel,
		"[tab] switch panel",
		"[↑/↓ · pgup/pgdn] scroll",
		"[q]uit",
	)
	left := styleMuted.Render(strings.Join(keys, "  "))
	right := ""
	// While any RPC fetch is in flight we show a spinning Braille dot
	// so the user can see the TUI is alive and talking to the daemon.
	// When all fetches settle the right side goes blank, a brief flash
	// every refresh interval rather than a persistent clock.
	if m.inflight > 0 {
		right = styleAccent.Render(spinnerFrames[m.spinnerFrame] + " refreshing")
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if gap < 1 {
		gap = 1
	}
	line := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
	return styleBorder.Width(m.width - 2).Render(line)
}

func (m Model) renderSendModal() string {
	var body string
	switch m.send.step {
	case sendStepAddress:
		body = "Recipient address:\n\n" + m.send.address.View()
		if m.send.validating {
			body += "\n\n" + styleMuted.Render("validating…")
		} else if m.send.errMsg != "" {
			body += "\n\n" + styleBad.Render(m.send.errMsg)
		} else {
			body += "\n\n" + styleMuted.Render("enter to validate · esc to cancel")
		}
	case sendStepAmount:
		body = "Amount (GRC):\n\n" + m.send.amount.View()
		avail := FormatGRCPlain(m.wallet.Balance)
		if m.anonymous {
			avail = MaskedAmount
		}
		body += "\n\n" + styleMuted.Render("available: "+avail)
		if m.send.errMsg != "" {
			body += "\n\n" + styleBad.Render(m.send.errMsg)
		} else {
			body += "\n\n" + styleMuted.Render("enter to continue · backspace to go back · esc to cancel")
		}
	case sendStepPassphrase:
		body = "Wallet is locked. Passphrase:\n\n" + m.send.passphrase.View()
		if m.send.errMsg != "" {
			body += "\n\n" + styleBad.Render(m.send.errMsg)
		} else {
			body += "\n\n" + styleMuted.Render("enter to continue · esc to cancel")
		}
	case sendStepConfirm:
		confirmAmount := FormatGRCFullPlain(m.send.amountValue)
		if m.anonymous {
			confirmAmount = MaskedAmount
		}
		body = styleTitle.Render("Confirm send") + "\n\n"
		body += fmt.Sprintf("  To:     %s\n", m.send.address.Value())
		body += fmt.Sprintf("  Amount: %s\n", confirmAmount)
		body += "\n" + styleMuted.Render("[y] broadcast   [n] cancel")
	case sendStepResult:
		if m.send.resultErr != "" {
			body = styleBad.Render("send failed") + "\n\n" + m.send.resultErr
		} else {
			body = styleGood.Render("sent ✓") + "\n\n" + "txid: " + m.send.resultTxID
		}
		body += "\n\n" + styleMuted.Render("press any key to close")
	}

	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(60).
		Render(styleTitle.Render("Send GRC") + "\n\n" + body)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// renderSignModal walks the sign-message wizard. Layout invariant: from
// the message step onwards, the chosen signing address is rendered as a
// persistent "Signing as: …" header at the top of the modal so the user
// can never sign, or read a signature, without seeing which key was
// used. The address-input step suppresses the header because the input
// field itself is the source of truth there.
func (m Model) renderSignModal() string {
	var body string
	switch m.sign.step {
	case signStepAddress:
		body = "Address to sign with:\n\n" + m.sign.address.View()
		if m.sign.errMsg != "" {
			body += "\n\n" + styleBad.Render(m.sign.errMsg)
		} else {
			body += "\n\n" + styleMuted.Render("enter to continue · esc to cancel")
		}
	case signStepMessage:
		body = "Message:\n\n" + m.sign.message.View()
		if m.sign.errMsg != "" {
			body += "\n\n" + styleBad.Render(m.sign.errMsg)
		} else {
			body += "\n\n" + styleMuted.Render("enter to sign · backspace to go back · esc to cancel")
		}
	case signStepPassphrase:
		body = "Wallet is locked. Passphrase:\n\n" + m.sign.passphrase.View()
		if m.sign.errMsg != "" {
			body += "\n\n" + styleBad.Render(m.sign.errMsg)
		} else {
			body += "\n\n" + styleMuted.Render("enter to sign · esc to cancel")
		}
	case signStepResult:
		if m.sign.resultErr != "" {
			body = styleBad.Render("sign failed") + "\n\n" + m.sign.resultErr
		} else {
			body = styleGood.Render("signed ✓") + "\n\n" +
				styleLabel.Render("Message:") + "\n" + m.sign.message.Value() + "\n\n" +
				styleLabel.Render("Signature (base64):") + "\n" + m.sign.resultSig
		}
		body += "\n\n" + styleMuted.Render("press any key to close")
	}
	if m.sign.busy {
		body += "\n\n" + styleMuted.Render("signing…")
	}

	header := styleTitle.Render("Sign message")
	// From the message step onwards, surface the signing address so it is
	// always visible. Skipping it on signStepAddress avoids a redundant
	// echo of the input field one line below.
	if m.sign.step != signStepAddress {
		addr := m.sign.address.Value()
		if addr == "" {
			addr = styleMuted.Render("(no address set)")
		} else {
			addr = styleAccent.Render(addr)
		}
		header += "\n" + styleLabel.Render("Signing as: ") + addr
	}

	// Default width is comfortable for the input steps. On the result
	// step we expand to whatever the signature needs so it fits on a
	// single uninterrupted line, otherwise lipgloss wraps it inside the
	// modal and a mouse selection drags the right-side border in with
	// the copied text. 6 = 2 border + 4 padding(1,2). Cap to the
	// terminal width so we still render cleanly on narrow terminals
	// (signature will wrap there as a last resort, but most terminals
	// are wide enough).
	modalWidth := 72
	if m.sign.step == signStepResult && m.sign.resultSig != "" {
		if needed := len(m.sign.resultSig) + 6; needed > modalWidth {
			modalWidth = needed
		}
	}
	if max := m.width - 2; modalWidth > max && max > 0 {
		modalWidth = max
	}

	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(modalWidth).
		Render(header + "\n\n" + body)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// renderEditLabelModal shows the address whose label is being edited (read
// only) plus the editable label input. Mirrors renderSignModal's double
// border centered layout, minus the multi-step machinery.
func (m Model) renderEditLabelModal() string {
	header := styleTitle.Render("Edit label") + "\n" +
		styleLabel.Render("Address: ") + styleAccent.Render(m.edit.address)

	body := "Label:\n\n" + m.edit.label.View()
	if m.edit.errMsg != "" {
		body += "\n\n" + styleBad.Render(m.edit.errMsg)
	} else {
		body += "\n\n" + styleMuted.Render("enter to save · empty clears label · esc to cancel")
	}
	if m.edit.busy {
		body += "\n\n" + styleMuted.Render("saving…")
	}

	modalWidth := 72
	if max := m.width - 2; modalWidth > max && max > 0 {
		modalWidth = max
	}

	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(modalWidth).
		Render(header + "\n\n" + body)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// renderTxDetailModal shows the full raw data for the currently selected
// transaction: full txid, full address, exact amount (8 decimals), block
// hash, absolute timestamp, and status. It is read-only, any key closes
// it (handled in update.go::handleKey / case modeTxDetail).
func (m Model) renderTxDetailModal() string {
	if m.txCursor < 0 || m.txCursor >= len(m.txs) {
		return m.renderDashboard()
	}
	tx := m.txs[m.txCursor]
	st := ClassifyTransaction(tx)

	field := func(label, value string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			styleLabel.Width(14).Render(label),
			styleValue.Render(value),
		)
	}

	kindStyle, ok := txKindStyle[st.Kind]
	if !ok {
		kindStyle = styleMuted
	}
	statusLine := field("Status", kindStyle.Render(st.Icon+" "+st.Label))

	addr := tx.Address
	if addr == "" && (tx.Category == "generate" || tx.Category == "immature") {
		addr = "(stake reward, no counterparty address)"
	} else if addr == "" {
		addr = "—"
	}

	timeLine := "—"
	if tx.Time > 0 {
		ts := time.Unix(tx.Time, 0)
		timeLine = ts.Format("2006-01-02 15:04:05 MST") + "  (" + FormatRelativeTime(tx.Time) + ")"
	}

	confLine := fmt.Sprintf("%d", tx.Confirmations)
	if tx.Confirmations < 0 {
		confLine += "  (in conflict)"
	} else if tx.Confirmations == 0 {
		confLine += "  (in mempool)"
	}

	amountStr := FormatGRCFull(tx.Amount)
	feeStr := FormatGRCFull(tx.Fee)
	if m.anonymous {
		amountStr = MaskedAmount
		feeStr = MaskedAmount
	}

	lines := []string{
		styleTitle.Render("Transaction"),
		"",
		statusLine,
		field("Category", tx.Category),
		field("Amount", amountStr),
	}
	if tx.Fee != 0 {
		lines = append(lines, field("Fee", feeStr))
	}
	lines = append(lines,
		field("Address", addr),
		field("TxID", tx.TxID),
		field("Confirmations", confLine),
		field("Time", timeLine),
	)
	if tx.BlockHash != "" {
		lines = append(lines, field("Block hash", tx.BlockHash))
	}
	if tx.Comment != "" {
		lines = append(lines, field("Comment", tx.Comment))
	}
	lines = append(lines, "", styleMuted.Render("enter/esc to close"))

	width := m.width - 8
	if width > 96 {
		width = 96
	}
	if width < 40 {
		width = 40
	}
	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(width).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) renderConfigModal() string {
	row := func(label string, field configField, value string) string {
		prefix := "  "
		labelStyle := configLabelStyle
		if m.conf.focused == field {
			prefix = styleAccent.Render("▸ ")
			labelStyle = configLabelFocused
			value = configValueFocused.Render(value)
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, prefix, labelStyle.Render(label), value)
	}

	networkValue := "mainnet"
	if m.conf.testnet {
		networkValue = "testnet"
	}
	networkLine := row("Network", cfgFieldNetwork,
		networkValue+"  "+styleMuted.Render("(space/←→ to toggle)"))

	hostLine := row("Host", cfgFieldHost, m.conf.host.View())
	portLine := row("Port", cfgFieldPort, m.conf.port.View())
	userLine := row("User", cfgFieldUser, m.conf.user.View())
	refreshLine := row("Refresh", cfgFieldRefresh, m.conf.refresh.View())

	// Password is read-only, we only show whether it was resolved from
	// flag/env/conf at startup. This keeps the passphrase off screen and
	// saves the user from re-typing it to tweak unrelated fields.
	passStatus := styleMuted.Render("not set")
	if m.cfg.Password != "" {
		passStatus = styleGood.Render("● set (read-only)")
	}
	passLine := lipgloss.JoinHorizontal(lipgloss.Top,
		"  ",
		configLabelStyle.Render("Password"),
		passStatus,
	)

	applyPrefix := "  "
	applyLabel := styleMuted.Render("[ Apply ]")
	if m.conf.focused == cfgFieldApply {
		applyPrefix = styleAccent.Render("▸ ")
		applyLabel = styleAccent.Render("[ Apply ]")
	}
	applyLine := lipgloss.JoinHorizontal(lipgloss.Top, applyPrefix, strings.Repeat(" ", 12), applyLabel)

	srcLine := ""
	if m.cfg.ConfPath != "" {
		srcLine = styleMuted.Render("loaded from: " + m.cfg.ConfPath)
	} else {
		srcLine = styleMuted.Render("no conf file read; values from flags/env/defaults")
	}

	errLine := ""
	if m.conf.errMsg != "" {
		errLine = "\n" + styleBad.Render(m.conf.errMsg)
	}

	hint := styleMuted.Render("tab/↓ next · shift+tab/↑ prev · enter on Apply to save · esc to cancel")

	body := lipgloss.JoinVertical(lipgloss.Left,
		networkLine,
		hostLine,
		portLine,
		userLine,
		passLine,
		refreshLine,
		"",
		applyLine,
		"",
		srcLine,
	) + errLine + "\n\n" + hint

	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(68).
		Render(styleTitle.Render("Config") + "\n\n" + body)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

