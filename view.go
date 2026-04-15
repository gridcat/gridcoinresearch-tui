// This file renders the current Model to a string that Bubble Tea writes to
// the terminal. Every frame of the TUI is produced by View() calling one of
// the render* helpers below. Keep in mind:
//
//   • View is a value receiver — it is pure, it cannot mutate state, and
//     Bubble Tea is free to call it as often as it likes.
//
//   • We use lipgloss for styling. A lipgloss.Style is a reusable config:
//     .Foreground(color), .Bold(true), .Width(n), .Border(…), .Padding(…),
//     then .Render(string) to get the final ANSI-coloured text.
//
//   • lipgloss.JoinHorizontal / JoinVertical place already-rendered blocks
//     next to each other — they measure the blocks, align them, and return
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
	case modeConfig:
		return m.renderConfigModal()
	case modeTxDetail:
		return m.renderTxDetailModal()
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
	// of the vertical budget. Addresses are a reference panel — capped at a
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

func (m Model) renderStats() string {
	if !m.loaded {
		return styleBorder.Width(m.width - 2).Render(styleMuted.Render("loading wallet…"))
	}

	balanceRow := statRow("Balance", styleValue.Render(FormatGRCPlain(m.wallet.Balance)),
		"Staking", m.stakingBadge())
	unconfRow := statRow("Unconfirmed", styleValue.Render(FormatGRCPlain(m.wallet.UnconfirmedBalance)),
		"Wallet", m.lockBadge())
	immatureRow := statRow("Immature", styleValue.Render(FormatGRCPlain(m.wallet.ImmatureBalance)),
		"Difficulty", styleValue.Render(fmt.Sprintf("%.4f", m.staking.Difficulty.Value())))

	content := lipgloss.JoinVertical(lipgloss.Left, balanceRow, unconfRow, immatureRow)
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
	if *m.wallet.UnlockedUntil == 0 {
		return styleWarn.Render("● locked")
	}
	remaining := time.Until(time.Unix(*m.wallet.UnlockedUntil, 0))
	return styleGood.Render("● unlocked " + FormatDuration(remaining))
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
		return box.Render(styleTitle.Render(title) + "\n" + styleMuted.Render("wallet has no addresses yet — run `getnewaddress`"))
	}

	// Available data rows inside the box: maxHeight - 2 (borders) - 1 (title row).
	maxRows := maxHeight - 3
	if maxRows < 1 {
		maxRows = 1
	}

	// Derive the window offset from the cursor — same pattern as renderTxList.
	offset := 0
	if m.addrCursor >= maxRows {
		offset = m.addrCursor - maxRows + 1
	}

	// Title carries the total count plus a "cursor/total" indicator when
	// the list is longer than the window.
	header := fmt.Sprintf("My Addresses (%d)", len(m.addresses))
	if len(m.addresses) > maxRows {
		header += fmt.Sprintf("  %d/%d", m.addrCursor+1, len(m.addresses))
	}
	lines := []string{styleTitle.Render(header)}

	end := offset + maxRows
	if end > len(m.addresses) {
		end = len(m.addresses)
	}
	for i := offset; i < end; i++ {
		prefix := "  "
		row := renderAddressRow(m.addresses[i])
		if i == m.addrCursor && m.focusedArea == focusAddr {
			prefix = styleAccent.Render("▸ ")
			row = styleRowSelected.Render(row)
		}
		lines = append(lines, prefix+row)
	}
	return box.Render(strings.Join(lines, "\n"))
}

func renderAddressRow(a ReceivedAddress) string {
	addr := styleValue.Render(a.Address)
	label := ""
	if l := a.DisplayLabel(); l != "" {
		label = "  " + styleMuted.Render(l)
	}
	amount := ""
	if a.Amount > 0 {
		amount = "  " + styleGood.Render("received "+FormatGRCPlain(a.Amount))
	}
	return "  " + addr + label + amount
}


// renderTxList draws the scrollable transactions panel, sized to fill the
// vertical space that renderDashboard handed it. Scroll math:
//
//   txCursor  — index of the currently selected tx in m.txs
//   offset    — index of the tx shown at the top of the visible window
//               (derived fresh every frame from cursor + maxRows)
//   maxRows   — how many data rows fit inside the box this frame
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
		line := renderTxRow(m.txs[i])
		if i == m.txCursor && m.focusedArea == focusTx {
			// Highlight only the focused panel's cursor row. An unfocused
			// tx list leaves the cursor as a silent bookmark — symmetric
			// with the addresses panel so the two behave the same.
			prefix = styleAccent.Render("▸ ")
			line = styleRowSelected.Render(line)
		}
		lines = append(lines, prefix+line)
	}
	return boxStyle.Render(strings.Join(lines, "\n"))
}

func renderTxRow(tx Transaction) string {
	st := ClassifyTransaction(tx)
	iconStyle, ok := txKindStyle[st.Kind]
	if !ok {
		iconStyle = styleMuted
	}
	icon := iconStyle.Render(st.Icon)
	status := styleTxStatusCol.Render(st.Label)

	amountStyle := styleValue
	switch {
	case tx.Amount < 0:
		amountStyle = styleWarn
	case tx.Amount > 0:
		amountStyle = styleGood
	}
	amountCol := styleTxAmountCol.Render(amountStyle.Render(FormatGRC(tx.Amount)))

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
	keys := []string{
		"[s]end",
		"[c]onfig",
		"[r]efresh",
		"[tab] switch panel",
		"[↑/↓ · pgup/pgdn] scroll",
		"[q]uit",
	}
	left := styleMuted.Render(strings.Join(keys, "  "))
	right := ""
	// While any RPC fetch is in flight we show a spinning Braille dot
	// so the user can see the TUI is alive and talking to the daemon.
	// When all fetches settle the right side goes blank — a brief flash
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
		body += "\n\n" + styleMuted.Render("available: "+FormatGRCPlain(m.wallet.Balance))
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
		body = styleTitle.Render("Confirm send") + "\n\n"
		body += fmt.Sprintf("  To:     %s\n", m.send.address.Value())
		body += fmt.Sprintf("  Amount: %s\n", FormatGRCFullPlain(m.send.amountValue))
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

// renderTxDetailModal shows the full raw data for the currently selected
// transaction: full txid, full address, exact amount (8 decimals), block
// hash, absolute timestamp, and status. It is read-only — any key closes
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
		addr = "(stake reward — no counterparty address)"
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

	lines := []string{
		styleTitle.Render("Transaction"),
		"",
		statusLine,
		field("Category", tx.Category),
		field("Amount", FormatGRCFull(tx.Amount)),
	}
	if tx.Fee != 0 {
		lines = append(lines, field("Fee", FormatGRCFull(tx.Fee)))
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

	// Password is read-only — we only show whether it was resolved from
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

