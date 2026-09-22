package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// renderTxDetailModal shows the full raw data for the currently selected
// transaction: full txid, full address, exact amount (8 decimals), block
// hash, absolute timestamp, and status. It is read-only, any key closes
// it (handled in app_keys.go::handleKey / case modeTxDetail).
func (m Model) renderTxDetailModal() string {
	if m.txCursor < 0 || m.txCursor >= len(m.txs) {
		return m.renderDashboard()
	}
	tx := m.txs[m.txCursor]
	st := format.ClassifyTransaction(tx)

	field := func(label, value string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			theme.Label.Width(14).Render(label),
			theme.Value.Render(value),
		)
	}

	kindStyle, ok := theme.TxKindStyle[st.Kind]
	if !ok {
		kindStyle = theme.Muted
	}
	statusLine := field("Status", kindStyle.Render(st.Icon+" "+st.Label))

	// Daemon-sourced values are sanitized at each call site rather than
	// inside field: field also receives values we rendered ourselves (the
	// Status line above, the muted "resolving…" below), and sanitizing those
	// would strip our own SGR colour escapes along with the hostile ones.
	// The IsContractCandidate branches below test tx.Address BEFORE
	// sanitizing, so a category/address forged out of control bytes can't
	// flip a real payment into the address-less contract branch.
	addr := format.SanitizeTerminal(tx.Address)
	if tx.Address == "" && (tx.Category == "generate" || tx.Category == "immature") {
		addr = "(stake reward, no counterparty address)"
	} else if format.IsContractCandidate(tx) {
		// Deliberately does not call it a contract: that claim belongs to the
		// Contract field above, which only makes it once the daemon has
		// confirmed one. All we know from listsinceblock alone is that the
		// output had nowhere to go.
		addr = "(burned, no destination address)"
	} else if addr == "" {
		addr = "—"
	}

	timeLine := "—"
	if tx.Time > 0 {
		ts := time.Unix(tx.Time, 0)
		timeLine = ts.Format("2006-01-02 15:04:05 MST") + "  (" + format.FormatRelativeTime(tx.Time) + ")"
	}

	confLine := fmt.Sprintf("%d", tx.Confirmations)
	if tx.Confirmations < 0 {
		confLine += "  (in conflict)"
	} else if tx.Confirmations == 0 {
		confLine += "  (in mempool)"
	}

	amountStr := format.FormatGRCFull(tx.Amount)
	feeStr := format.FormatGRCFull(tx.Fee)
	if m.anonymous {
		amountStr = format.MaskedAmount
		feeStr = format.MaskedAmount
	}

	lines := []string{
		statusLine,
		field("Category", format.SanitizeTerminal(tx.Category)),
	}
	// What this "send" actually was. The daemon's own category can't say;
	// the contract is only visible via gettransaction, normally already
	// fetched by the txsMsg batch and otherwise asked for when this modal
	// opens (see app_keys.go, case "enter"). Three states: a known type,
	// still-resolving, and resolved-as-no-contract, which drops the line.
	if format.IsContractCandidate(tx) {
		if ctype, ok := m.txContracts[tx.TxID]; !ok {
			lines = append(lines, field("Contract", theme.Muted.Render("resolving…")))
		} else if ctype != "" {
			lines = append(lines, field("Contract", format.SanitizeTerminal(ctype)))
		}
	}
	lines = append(lines, field("Amount", amountStr))
	if tx.Fee != 0 {
		lines = append(lines, field("Fee", feeStr))
	}
	lines = append(lines, field("Address", addr))
	// listsinceblock does not include address-book labels. Match its address
	// against the cached listreceivedbyaddress response so a normal payment's
	// detail popup identifies a saved recipient without another RPC request.
	// Address-less stake and contract entries naturally have no match.
	if label := m.addressLabel(tx.Address); label != "" {
		lines = append(lines, field("Label", format.SanitizeTerminal(label)))
	}
	lines = append(lines,
		field("TxID", format.SanitizeTerminal(tx.TxID)),
		field("Confirmations", confLine),
		field("Time", timeLine),
	)
	if tx.BlockHash != "" {
		lines = append(lines, field("Block hash", format.SanitizeTerminal(tx.BlockHash)))
	}
	if tx.Comment != "" {
		lines = append(lines, field("Comment", format.SanitizeTerminal(tx.Comment)))
	}
	lines = append(lines, "", theme.Muted.Render("enter/esc to close"))

	width := m.width - 8
	if width > 96 {
		width = 96
	}
	if width < 40 {
		width = 40
	}
	modal := ui.ModalBox(width, "Transaction", strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// handleTxDetailKey drives the read-only transaction detail modal: any of
// these keys closes it and returns to the dashboard.
func (m Model) handleTxDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k := msg.String(); k == "esc" || k == "q" || k == "enter" {
		m.mode = modeDashboard
	}
	return m, nil
}
