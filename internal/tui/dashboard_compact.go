package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
	"github.com/mattn/go-runewidth"
)

// renderCompactDashboard is the dashboard for terminals too small for the
// full layout (see compactFor), down to minWidth×minHeight. It saves rows the
// way a phone layout does:
//
//	╭─ my-wallet ─ ● main ─ #3,500,000 ─ 8⇅ ─╮  header folded into the edge
//	│ Bal  123,456.78      ● staking ~1d     │  zero rows hidden, statuses
//	│ Tot  123,478.78      ◐ unlocked 59m    │  as icons
//	╰────────────────────────────────────────╯
//	╭─ Transactions ─────────────────────────╮  one list at a time: Tab
//	│ ▸ ✓  −1,234.50  5m  SGrcPa…QpZ         │  swaps it for My Addresses
//	╰───────────────────────────────── 1/30 ─╯
//	 ?help s send r ref ⇥ addrs q quit        one borderless key line
func (m Model) renderCompactDashboard() string {
	stats := m.renderCompactStats()
	height := m.compactBodyHeight(stats)
	var body string
	if m.focusedArea == focusAddr {
		body = m.renderAddresses(height, true)
	} else {
		body = m.renderTxList(height, true)
	}
	return lipgloss.JoinVertical(lipgloss.Left, stats, body, m.renderCompactFooter())
}

// compactBodyHeight is what the compact layout leaves for its one list: the
// terminal minus the stats box and the one-line footer.
func (m Model) compactBodyHeight(stats string) int {
	h := m.height - lipgloss.Height(stats) - 1
	if h < 3 {
		h = 3
	}
	return h
}

// compactTitle is the header text for the stats box's top edge: wallet name
// (if any), network, block height, peers, and an arrow when an update is out.
// The program name and version are left out; "?" and "u" show them. A network
// mismatch replaces the whole line in red, because nothing else on it matters
// until that is fixed.
func (m Model) compactTitle() (string, lipgloss.Style) {
	if warn := m.networkMismatch(); warn != "" {
		return warn, theme.Bad
	}
	var parts []string
	if name := m.walletName(); name != "" {
		parts = append(parts, name)
	}
	network := theme.GlyphOn + " main"
	if m.cfg.Testnet {
		network = theme.GlyphOn + " test"
	}
	parts = append(parts, network)
	if m.chain.Blocks > 0 {
		parts = append(parts, "#"+format.GroupThousandsInt64(m.chain.Blocks))
	}
	if m.peersLoaded {
		parts = append(parts, fmt.Sprintf("%d%s", m.peersTotal, theme.GlyphPeers))
	}
	if m.updateAvailable && m.latestVersion != "" {
		parts = append(parts, theme.GlyphUpdate)
	}
	return strings.Join(parts, " ─ "), theme.Title
}

// compactStatus is one right-column entry of the compact stats box: plain
// text, so it can be truncated to the column before it is coloured.
type compactStatus struct {
	text  string
	style lipgloss.Style
}

// renderCompactStats is renderStats for the compact layout. Amounts go in
// the left column with 3-letter labels and no unit; Balance and Total always
// show, the rest only when non-zero. Statuses go in the right column as
// icons. Difficulty is left out: it is the least useful number on a small
// screen.
func (m Model) renderCompactStats() string {
	box := theme.Border.Width(m.width - 2)
	title, titleStyle := m.compactTitle()
	if !m.loaded {
		return ui.TitledBox(box, titleStyle, title, theme.Muted.Render("loading wallet…"))
	}

	amount := func(v float64) string {
		if m.anonymous {
			return "••••••"
		}
		return strings.TrimSuffix(format.FormatGRCPlain(v), " GRC")
	}
	// Same sums as renderStats, so both layouts always agree.
	unconfirmed := m.wallet.UnconfirmedBalance + m.unconfirmedReceived()
	total := m.wallet.Balance + m.wallet.Stake + unconfirmed + m.wallet.ImmatureBalance
	pending := 0.0
	if m.staking.Magnitude != nil && m.staking.PendingReward != nil {
		pending = *m.staking.PendingReward
	}
	var left []string
	addAmount := func(label string, v float64, always bool) {
		if v == 0 && !always {
			return
		}
		left = append(left, theme.Label.Render(label+" ")+theme.Value.Render(ui.FixedCell(amount(v), 14, true)))
	}
	addAmount("Bal", m.wallet.Balance, true)
	addAmount("Unc", unconfirmed, false)
	addAmount("Imm", m.wallet.ImmatureBalance, false)
	addAmount("Stk", m.wallet.Stake, false)
	addAmount("Tot", total, true)
	addAmount("Rwd", pending, false)

	// The left column is "Bal " + 14 = 18 wide, then a 3-column gap; the
	// right column gets the rest of the box's inner width (m.width-4).
	const leftWidth, gap = 18, 3
	rightWidth := m.width - 4 - leftWidth - gap
	var right []string
	for _, st := range m.compactStatuses() {
		right = append(right, st.style.Render(ui.Truncate(st.text, rightWidth)))
	}

	rows := make([]string, 0, len(left))
	for i := 0; i < len(left) || i < len(right); i++ {
		l, r := strings.Repeat(" ", leftWidth), ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		rows = append(rows, l+strings.Repeat(" ", gap)+r)
	}
	if m.walletErr != "" {
		rows = append(rows, theme.Bad.Render(ui.Truncate(theme.GlyphError+" "+format.SanitizeTerminal(m.walletErr), m.width-4)))
	}
	return ui.TitledBox(box, titleStyle, title, strings.Join(rows, "\n"))
}

// compactStatuses is the right column of the compact stats box: staking,
// wallet lock, cruncher/investor and magnitude, in the colours renderStats
// uses for the same facts.
func (m Model) compactStatuses() []compactStatus {
	var out []compactStatus
	switch {
	case m.staking.Staking:
		text := theme.GlyphOn + " staking"
		if eta := format.FormatStakeETA(m.staking.ExpectedTime); eta != "—" {
			text += " ~" + eta
		}
		out = append(out, compactStatus{text, theme.Good})
	case m.staking.MiningError != "":
		out = append(out, compactStatus{theme.GlyphOff + " " + format.SanitizeTerminal(m.staking.MiningError), theme.Warn})
	default:
		out = append(out, compactStatus{theme.GlyphOff + " not staking", theme.Muted})
	}

	switch {
	case m.wallet.UnlockedUntil == nil:
		out = append(out, compactStatus{theme.GlyphOff + " unencrypted", theme.Muted})
	case m.wallet.IsLocked():
		out = append(out, compactStatus{theme.GlyphLocked + " locked", theme.Warn})
	default:
		remaining := time.Until(time.Unix(*m.wallet.UnlockedUntil, 0))
		out = append(out, compactStatus{theme.GlyphUnlocked + " unlocked " + format.FormatDuration(remaining), theme.Good})
	}

	// An empty CPID is "not known yet", not "investor"; see researcherBadge.
	switch {
	case m.staking.CPID == "":
	case !m.staking.IsCruncher():
		out = append(out, compactStatus{theme.GlyphInvestor + " investor", theme.Muted})
	default:
		out = append(out, compactStatus{theme.GlyphCruncher + " " + format.ShortAddress(m.staking.CPID), theme.Good})
	}
	if m.staking.Magnitude != nil {
		out = append(out, compactStatus{fmt.Sprintf("M %.2f", *m.staking.Magnitude), theme.Value})
	}
	return out
}

// renderCompactFooter is the compact layout's borderless key line: help,
// send, refresh, quit and where Tab goes, with the refresh spinner in the
// last column. The other keys are listed under "?".
func (m Model) renderCompactFooter() string {
	other := "addrs"
	if m.focusedArea == focusAddr {
		other = "txs"
	}
	keys := "?help s send r ref " + theme.GlyphTab + " " + other + " q quit"
	if m.anonymous {
		keys += " a" + theme.GlyphOn
	}
	spin := " "
	if m.inflight > 0 {
		spin = ui.SpinnerFrames[m.spinnerFrame]
	}
	// One leading space, the keys, then the spinner in the last column.
	keys = ui.Truncate(keys, m.width-3)
	pad := m.width - 2 - runewidth.StringWidth(keys)
	if pad < 1 {
		pad = 1
	}
	return " " + theme.Muted.Render(keys) + strings.Repeat(" ", pad) + theme.Accent.Render(spin)
}
