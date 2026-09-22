package tui

import (
	"math"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// txRefreshDepth is how many blocks back listsinceblock holds its cursor,
// i.e. how deep a transaction stays in the per-tick refresh window. It has to
// exceed Gridcoin's coinstake maturity (~100 blocks on mainnet) so a stake
// keeps getting re-fetched for its whole immature life and we catch it flip
// from category "immature" to "generate" when it matures. The old value of 6
// (our confirmed-depth threshold) was far too shallow: a stake left the window
// after ~6 blocks but stays immature for ~100, so its cached "immature"
// category went stale and never updated until a full re-seed. 120 covers
// mainnet maturity with margin; on testnet (maturity ~10) it just re-reads a
// few extra blocks, which is harmless.
const txRefreshDepth = 120

// fetchTxs fetches transaction deltas via listsinceblock. The cursor from
// the previous successful fetch is passed in; on the very first call it
// is the empty string and the daemon returns the full wallet history.
func fetchTxs(c *rpc.Client, lastBlock string) tea.Cmd {
	return func() tea.Msg {
		resp, err := c.ListSinceBlock(lastBlock, txRefreshDepth, true)
		return txsMsg{resp: resp, err: err}
	}
}

// fetchContracts resolves the Gridcoin contract type of each given
// transaction via gettransaction, one txid at a time, the same serial
// good-neighbour policy as fetchAddrOwnership above, for the same reason.
//
// We need it because listsinceblock reports a beacon advertisement or a vote
// as an ordinary "send" and never mentions the contract riding along with it
// (see IsContractCandidate); gettransaction is the only wallet RPC that
// decodes it. Callers pass only txids that aren't cached yet, and a mined
// contract never changes, so in the steady state this stays quiet.
//
// "Not cached yet" is not the same as "not already being fetched": the cache
// only fills when the reply lands, so a tick or a modal open during a long
// initial batch can start a second lookup for the same txid. The results are
// identical and merge idempotently, which is why this is left unguarded,
// the same trade-off fetchAddrOwnership already makes.
//
// A txid whose lookup fails is left out of the map entirely rather than
// cached as "no contract": leaving it unresolved means the next wallet
// activity (or the user opening its detail modal) retries it, where a
// wrong negative answer would stick for the whole session.
func fetchContracts(c *rpc.Client, txids []string) tea.Cmd {
	return func() tea.Msg {
		types := make(map[string]string, len(txids))
		for _, id := range txids {
			d, err := c.GetTransaction(id)
			if err != nil {
				continue
			}
			// The empty string is a real answer here ("we asked, there is no
			// contract"), which is what stops us asking again every tick.
			//
			// Only the first contract is read. The field is a vector, but the
			// daemon treats one-per-transaction as the rule and indexes
			// vContracts[0] the same way throughout its own miner, voting and
			// wallet code; block validation even rejects a coinbase carrying
			// more than one.
			var t string
			if len(d.Contracts) > 0 {
				t = d.Contracts[0].Type
			}
			types[id] = t
		}
		return txContractsMsg{types}
	}
}

// txKey is the composite identity of a Transaction entry, used to update an
// entry in place across refreshes instead of duplicating it. It has to
// distinguish entries from the same on-chain tx that differ in output:
// gridcoinresearchd emits one entry per (tx, vout, recipient) tuple, so
// keying by txid alone would collapse multi-output transactions. Address and
// the signed amount keep them distinct (a self-send shows a negative "send"
// and a positive "receive" entry on the same txid). We store the amount as
// fixed-point satoshis (1 GRC = 1e8 sat) instead of a raw float64 so two
// entries with "the same" amount always compare equal: float representations
// of decimal amounts can round-trip differently across RPC calls and defeat a
// naive ==.
//
// Category is deliberately NOT in the key: it is mutable. A coinstake moves
// from "immature" to "generate" as it matures, and keying on category would
// treat the matured entry as brand new and append a duplicate instead of
// replacing the immature one in place.
type txKey struct {
	TxID      string
	Address   string
	AmountSat int64
}

func makeTxKey(tx rpc.Transaction) txKey {
	return txKey{
		TxID:      tx.TxID,
		Address:   tx.Address,
		AmountSat: int64(math.Round(tx.Amount * 1e8)),
	}
}

// mergeTransactions folds a delta list from listsinceblock into an
// existing sorted list. Entries are keyed by the txKey composite above:
// existing entries are updated in place (so confirmation counts tick up
// on every refresh), new ones are appended, and the result is sorted
// newest-first by Time, but only when an append actually happened.
// An idle wallet's listsinceblock response just re-asserts entries we
// already have, so hasNew stays false, the in-place updates preserve
// the existing ordering, and we skip the O(n log n) sort.
//
// The second return value reports whether any entry in the delta was
// genuinely new. Callers use that signal to trigger an addresses
// refresh only when there's actual wallet activity instead of polling
// the expensive listreceivedbyaddress RPC on every tick.
func mergeTransactions(existing, delta []rpc.Transaction) ([]rpc.Transaction, bool) {
	if len(delta) == 0 {
		return existing, false
	}
	index := make(map[txKey]int, len(existing)+len(delta))
	for i, tx := range existing {
		index[makeTxKey(tx)] = i
	}
	hasNew := false
	for _, tx := range delta {
		k := makeTxKey(tx)
		if idx, ok := index[k]; ok {
			existing[idx] = tx
		} else {
			existing = append(existing, tx)
			index[k] = len(existing) - 1
			hasNew = true
		}
	}
	if hasNew {
		sort.Slice(existing, func(i, j int) bool {
			if existing[i].Time != existing[j].Time {
				return existing[i].Time > existing[j].Time
			}
			// Tiebreaker on txid so txs that landed in the same block don't
			// flicker between frames when the map iteration order changes.
			return existing[i].TxID > existing[j].TxID
		})
	}
	return existing, hasNew
}

// renderTxList draws the scrollable transactions panel, sized to fill the
// vertical space that renderDashboard handed it. Scroll math:
//
//	txCursor: index of the currently selected tx in m.txs
//	offset: index of the tx shown at the top of the visible window
//	            (derived fresh every frame from cursor + maxRows)
//	maxRows: how many data rows fit inside the box this frame
//
// We slide offset just enough to keep the cursor in view.
func (m Model) renderTxList(height int) string {
	border := theme.Border
	if m.focusedArea == focusTx {
		border = theme.BorderFocused
	}
	boxStyle := border.Width(m.width - 2).Height(height - 2)
	titleStyle := theme.Title
	if m.focusedArea == focusTx {
		titleStyle = theme.Accent
	}
	box := func(content string) string {
		return ui.TitledBox(boxStyle, titleStyle, "Transactions", content)
	}
	if !m.txsLoaded {
		return box(theme.Muted.Render("loading…"))
	}
	if m.txsErr != "" {
		return box(theme.Bad.Render("error: " + format.SanitizeTerminal(m.txsErr)))
	}
	if len(m.txs) == 0 {
		return box(theme.Muted.Render("no transactions yet"))
	}

	maxRows, _ := ui.ListWindow(height, m.txCursor, len(m.txs))
	offset := m.txWindowOffset(maxRows)
	var lines []string
	for i := offset; i < offset+maxRows && i < len(m.txs); i++ {
		prefix := "  "
		// A missing cache entry yields "", the same value as a lookup that
		// came back with no type. The row renders both as a generic
		// "(contract)", which reads correctly either way: still resolving,
		// or a contract the daemon itself could not classify. Only the
		// detail modal needs to tell the two apart.
		tx := m.txs[i]
		line := renderTxRowLabeled(tx, m.anonymous, m.txContracts[tx.TxID], m.addressLabel(tx.Address))
		if i == m.txCursor && m.focusedArea == focusTx {
			// Highlight only the focused panel's cursor row. An unfocused
			// tx list leaves the cursor as a silent bookmark, symmetric
			// with the addresses panel so the two behave the same. Paint the
			// whole row via fillBackground (the same edge-to-edge highlight
			// the addresses panel uses).
			prefix = theme.Accent.Background(theme.ColorRowSelected).Render("▸ ")
			line = ui.FillBackground(line, m.panelRowWidth())
		}
		lines = append(lines, prefix+line)
	}
	return box(strings.Join(lines, "\n"))
}

// renderTxRow renders one transaction line. contractType is the cached
// Gridcoin contract kind for this txid ("beacon", "vote", …), or "" when it
// is unknown or the transaction carries no contract; see Model.txContracts.
func renderTxRow(tx rpc.Transaction, anonymous bool, contractType string) string {
	return renderTxRowLabeled(tx, anonymous, contractType, "")
}

// renderTxRowLabeled renders a transaction row with an optional saved
// counterparty label. The public wrapper above keeps callers that do not have
// the address book cache (and focused row-render tests) straightforward.
func renderTxRowLabeled(tx rpc.Transaction, anonymous bool, contractType, label string) string {
	var b strings.Builder
	for _, seg := range txRowSegments(tx, anonymous, contractType, label) {
		b.WriteString(seg.Style.Render(seg.Text))
	}
	return b.String()
}

// txRowSegments builds the coloured runs for a transaction row. The optional
// saved label is its own final column and is shortened to preserve the table's
// compact, single-line layout.
func txRowSegments(tx rpc.Transaction, anonymous bool, contractType, label string) []ui.Seg {
	st := format.ClassifyTransaction(tx)
	iconStyle, ok := theme.TxKindStyle[st.Kind]
	if !ok {
		iconStyle = theme.Muted
	}
	var amount string
	amountStyle := theme.Value.Width(18).Align(lipgloss.Right)
	if anonymous {
		amount = format.MaskedAmount
		amountStyle = theme.Muted.Width(18).Align(lipgloss.Right)
	} else {
		switch {
		case tx.Amount < 0:
			amountStyle = theme.Warn.Width(18).Align(lipgloss.Right)
		case tx.Amount > 0:
			amountStyle = theme.Good.Width(18).Align(lipgloss.Right)
		}
		amount = format.FormatGRC(tx.Amount)
	}

	addr := tx.Address
	if addr == "" && (tx.Category == "generate" || tx.Category == "immature") {
		addr = "(stake)"
	} else if format.IsContractCandidate(tx) {
		// A beacon or vote has no counterparty address, so this column would
		// otherwise be blank and the row would read as broken data. Name the
		// contract instead. Every label stays under ShortAddress's 12-char
		// eliding threshold ("(sidestake)", the longest of the daemon's
		// contract types, is 11), so these pass through it unaltered.
		if contractType == "" {
			contractType = "contract"
		}
		addr = "(" + contractType + ")"
	}
	// Sanitized after the label is assembled, so the daemon-supplied contract
	// type is covered along with tx.Address, and before ShortAddress so its
	// length check counts the characters that will actually be printed.
	segs := []ui.Seg{{Text: st.Icon, Style: iconStyle}, {Text: " ", Style: theme.Muted},
		{Text: ui.FixedCell(st.Label, 10, false), Style: theme.TxStatusCol}, {Text: ui.FixedCell(amount, 18, true), Style: amountStyle}, {Text: "  ", Style: theme.Muted},
		{Text: ui.FixedCell(format.ShortAddress(format.SanitizeTerminal(addr)), 16, false), Style: theme.TxAddrCol}, {Text: "  ", Style: theme.Muted},
		{Text: ui.FixedCell(format.FormatRelativeTime(tx.Time), 12, false), Style: theme.TxTimeCol.Foreground(theme.ColorMuted)}, {Text: "  ", Style: theme.Muted},
		{Text: ui.FixedCell(format.SanitizeTerminal(tx.Category), 10, false), Style: theme.Muted}}
	if tx.Address != "" && label != "" {
		// Keep the label column compact but give names substantially more room
		// than an address abbreviation. It is the final column, so widening it
		// never moves the aligned start of any other label.
		segs = append(segs, ui.Seg{Text: "  ", Style: theme.Muted}, ui.Seg{Text: ui.Truncate(format.SanitizeTerminal(label), 18), Style: theme.Muted})
	}
	return segs
}

// onTxsMsg handles txsMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onTxsMsg(msg txsMsg) (tea.Model, tea.Cmd) {
	m.finishFetch()
	// On error we still flip txsLoaded to true so the panel stops
	// saying "loading…" and starts showing the error instead.
	if msg.err != nil {
		m.txsErr = msg.err.Error()
		m.txsLoaded = true
		return m, nil
	}
	// Capture before we flip txsLoaded: the very first successful
	// fetch is the initial load, which fires concurrently with the
	// initial fetchAddrs from refreshAllCmd. Triggering an extra
	// address fetch on that first merge would duplicate work.
	alreadyLoaded := m.txsLoaded
	merged, hasNew := mergeTransactions(m.txs, msg.resp.Transactions)
	m.txs = merged
	m.txsLastBlock = msg.resp.LastBlock
	m.txsLoaded = true
	m.txsErr = ""
	m.txCursor = clampCursor(m.txCursor, len(m.txs))
	// Only chain an addresses refresh if a brand-new tx showed up
	// AFTER the initial load. Idle wallets produce hasNew=false on
	// every tick, so the expensive listreceivedbyaddress RPC stays
	// quiet until something actually changes.
	var cmds []tea.Cmd
	if alreadyLoaded && hasNew {
		cmds = append(cmds, fetchAddrs(m.rpc))
	}
	// Resolve the type of any contract transaction we haven't looked up
	// yet. Same "only when something changed" gate as the addresses
	// above, with one addition: the initial load (!alreadyLoaded) is the
	// pass that returns the entire wallet history, so it's where a
	// long-standing voter's backlog gets resolved. That one batch does
	// overlap the tail of refreshAllCmd's sequence (fetchTxs is its
	// fifth step, fetchAddrs its sixth), so startup can briefly hold two
	// RPC workers. Accepted: it happens once per launch, and the
	// gettransaction side is a wallet-map lookup rather than a scan.
	if !alreadyLoaded || hasNew {
		if ids := m.uncachedContractTxIDs(); len(ids) > 0 {
			cmds = append(cmds, fetchContracts(m.rpc, ids))
		}
	}
	if len(cmds) > 0 {
		// tea.Sequence, not tea.Batch, for the fetches: this is the only
		// handler that can dispatch two of them at once, and running
		// listreceivedbyaddress and a gettransaction walk concurrently
		// would hold two daemon RPC workers, the thing refreshAllCmd
		// goes out of its way to avoid. The spinner tick stays outside
		// the sequence: it isn't an RPC, and burying it behind the
		// fetches would delay the first frame until they finished.
		spin := m.bumpInflight(len(cmds))
		return m, tea.Batch(tea.Sequence(cmds...), spin)
	}
	return m, nil
}

// onTxContractsMsg handles txContractsMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onTxContractsMsg(msg txContractsMsg) (tea.Model, tea.Cmd) {
	m.finishFetch()
	for id, t := range msg.types {
		m.txContracts[id] = t
	}
	return m, nil
}

// uncachedContractTxIDs returns the txids of loaded transactions that look
// like contracts but whose type hasn't been resolved yet, so callers can
// fetch just those. The listsinceblock counterpart of unknownOwnership.
func (m Model) uncachedContractTxIDs() []string {
	var out []string
	for _, tx := range m.txs {
		if !format.IsContractCandidate(tx) {
			continue
		}
		if _, ok := m.txContracts[tx.TxID]; !ok {
			out = append(out, tx.TxID)
		}
	}
	return out
}
