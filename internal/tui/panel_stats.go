package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
)

// fetchWallet returns a Cmd that will call GetWalletInfo on a goroutine and
// turn the result into a walletMsg. Note the Cmd "captures" the rpc pointer
// in a closure, so when the Cmd runs later it still has access to it.
func fetchWallet(c *rpc.Client) tea.Cmd {
	return func() tea.Msg {
		w, err := c.GetWalletInfo()
		return walletMsg{w, err}
	}
}

func fetchStaking(c *rpc.Client) tea.Cmd {
	return func() tea.Msg {
		s, err := c.GetStakingInfo()
		return stakingMsg{s, err}
	}
}

func (m Model) renderStats() string {
	if !m.loaded {
		return theme.Border.Width(m.width - 2).Render(theme.Muted.Render("loading wallet…"))
	}

	fmtBal := func(v float64) string {
		if m.anonymous {
			return theme.Value.Render(format.MaskedAmount)
		}
		return theme.Value.Render(format.FormatGRCPlain(v))
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
		"Difficulty", theme.Value.Render(fmt.Sprintf("%.4f", m.staking.Difficulty.Value())))

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
	// The Total row's right half was the only always-rendered free slot, so
	// the cruncher/investor indicator lives there (issue #7). It matters that
	// it shows for investors too: the researcher row below only appears for
	// crunchers, so without this an investor could never tell whether the
	// wallet was mis-configured or simply isn't crunching. The label goes
	// away with the badge when neither is known yet.
	researcher := m.researcherBadge()
	researcherLabel := "Researcher"
	if researcher == "" {
		researcherLabel = ""
	}
	rows = append(rows, statRow("Total", fmtBal(total), researcherLabel, researcher))

	// Researcher stats (issue #7): pending research reward + magnitude.
	// getstakinginfo only carries these when a CPID is configured, so the
	// row disappears for investors. Placed after Total because the pending
	// reward is not part of the wallet's current holdings. The reward is an
	// amount and honours anonymous mode via fmtBal; magnitude is public
	// network data and stays visible. Note the row's mere presence still
	// reveals "this wallet crunches" in anonymous mode, which is within the
	// mode's contract (it hides monetary amounts, not identity).
	if m.staking.Magnitude != nil {
		pending := 0.0
		if m.staking.PendingReward != nil {
			pending = *m.staking.PendingReward
		}
		rows = append(rows, statRow("Pending Reward", fmtBal(pending),
			"Magnitude", theme.Value.Render(fmt.Sprintf("%.2f", *m.staking.Magnitude))))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, rows...)
	// The daemon controls rpcError.Message, so every error string that came
	// over RPC goes through sanitizeTerminal like any other daemon field.
	// Same treatment at every error line below.
	if m.walletErr != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, content, "", theme.Bad.Render("error: "+format.SanitizeTerminal(m.walletErr)))
	}
	return theme.Border.Width(m.width - 2).Render(content)
}

func statRow(labelA, valueA, labelB, valueB string) string {
	left := lipgloss.JoinHorizontal(lipgloss.Top,
		theme.StatLabelA.Render(labelA),
		theme.StatValueA.Render(valueA),
	)
	right := lipgloss.JoinHorizontal(lipgloss.Top,
		theme.StatLabelB.Render(labelB),
		valueB,
	)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
}

func (m Model) stakingBadge() string {
	if m.staking.Staking {
		label := "● yes"
		if eta := format.FormatStakeETA(m.staking.ExpectedTime); eta != "—" {
			label += " (~" + eta + ")"
		}
		return theme.Good.Render(label)
	}
	if m.staking.MiningError != "" {
		return theme.Warn.Render("○ " + format.SanitizeTerminal(m.staking.MiningError))
	}
	return theme.Muted.Render("○ no")
}

// researcherBadge says whether this wallet crunches for BOINC or is
// investor-only (issue #7). The CPID is truncated with the same helper the
// address columns use, which keeps the Total row inside 80 columns; a full
// 32-char digest would wrap it.
//
// An empty CPID returns an empty badge, and the caller drops the label with
// it. That case is "we don't know yet", not "investor": getstakinginfo is the
// third fetch of the startup sequence, so a badge that defaulted to investor
// would tell every cruncher the wrong thing for the first frames. It is also
// what the daemon itself sends when it cannot resolve an id at all.
//
// Deliberately visible in anonymous mode: a CPID is a public on-chain
// identifier, not a monetary amount, and the researcher row just below
// already treats magnitude the same way.
func (m Model) researcherBadge() string {
	switch {
	case m.staking.CPID == "":
		return ""
	case !m.staking.IsCruncher():
		return theme.Muted.Render("investor")
	default:
		return theme.Good.Render("cruncher ") + theme.Value.Render(format.ShortAddress(m.staking.CPID))
	}
}

func (m Model) lockBadge() string {
	if m.wallet.UnlockedUntil == nil {
		return theme.Muted.Render("● unencrypted")
	}
	if m.wallet.IsLocked() {
		return theme.Warn.Render("● locked")
	}
	remaining := time.Until(time.Unix(*m.wallet.UnlockedUntil, 0))
	return theme.Good.Render("● unlocked " + format.FormatDuration(remaining))
}

// unconfirmedReceived totals coins received but not yet confirmed enough to
// count toward Balance, the rows shown as "upcoming"/"incoming" in the tx
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
		switch format.ClassifyTransaction(tx).Kind {
		case format.TxStatusUpcoming, format.TxStatusIncoming:
			total += tx.Amount
		}
	}
	return total
}

// onWalletMsg handles walletMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onWalletMsg(msg walletMsg) (tea.Model, tea.Cmd) {
	m.finishFetch()
	if msg.err != nil {
		m.walletErr = msg.err.Error()
	} else {
		m.wallet = msg.w
		m.lastUpdate = time.Now()
		m.loaded = true
		m.walletErr = ""
	}
	return m, nil
}

// onStakingMsg handles stakingMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onStakingMsg(msg stakingMsg) (tea.Model, tea.Cmd) {
	m.finishFetch()
	if msg.err != nil {
		m.walletErr = msg.err.Error()
	} else {
		m.staking = msg.s
	}
	return m, nil
}
