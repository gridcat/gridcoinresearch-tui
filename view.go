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
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Colour scheme. lipgloss.Color accepts any 256-colour terminal code as a
// decimal string, and the terminal renders it via ANSI SGR. Where the
// terminal doesn't support colour, lipgloss strips the escape sequences.
//
// A scheme is a palette value plus an entry in the schemes map below. Nothing
// else needs touching to add one: buildStyles consumes the palette generically,
// so every style picks the new colours up automatically.
type palette struct {
	border      lipgloss.Color
	muted       lipgloss.Color
	label       lipgloss.Color
	value       lipgloss.Color
	title       lipgloss.Color
	accent      lipgloss.Color
	rowSelected lipgloss.Color // highlight background for the selected row

	// Status colours. A scheme is free to restyle these to fit its palette,
	// but the three must stay clearly distinguishable from each other and from
	// the chrome: they are the only cue for state rather than decoration, so
	// "staking ● yes" must never be mistakable for an error.
	good lipgloss.Color
	warn lipgloss.Color
	bad  lipgloss.Color

	// Network badge colours, used by the header's "● mainnet" / "● testnet".
	mainnet lipgloss.Color
	testnet lipgloss.Color
}

// schemes holds every selectable colour scheme. Add a scheme by adding an
// entry here and pointing something at its name.
var schemes = map[string]palette{
	// "default" is the original neutral look: grey chrome, blue accent.
	"default": {
		border:      lipgloss.Color("240"),
		muted:       lipgloss.Color("244"),
		label:       lipgloss.Color("250"),
		value:       lipgloss.Color("255"),
		title:       lipgloss.Color("255"),
		accent:      lipgloss.Color("75"), // blue
		rowSelected: lipgloss.Color("236"),
		good:        lipgloss.Color("42"),  // green
		warn:        lipgloss.Color("214"), // orange
		bad:         lipgloss.Color("203"), // red
		mainnet:     lipgloss.Color("42"),
		testnet:     lipgloss.Color("214"),
	},

	// "orange" is the testnet look, matching the orange-for-testnet convention
	// the *.gridcoin.club frontends use so a testnet window is unmistakable
	// among mainnet ones. The warm neutrals form a deliberate ramp — muted 137
	// < border 172 < label 180 < accent 208 < title 214 < value 230 — so text
	// hierarchy survives even though nearly everything is orange.
	//
	// The status colours are warmed too rather than left green/red, so nothing
	// on screen breaks the theme. They stay mutually distinct by hue instead of
	// by temperature: yellow 184 (good) / orange 214 (warn) / red-orange 202
	// (bad) still reads as a traffic light, just a warm one.
	"orange": {
		border:      lipgloss.Color("172"),
		muted:       lipgloss.Color("137"),
		label:       lipgloss.Color("180"),
		value:       lipgloss.Color("230"),
		title:       lipgloss.Color("214"), // ~ family testnet primary #ef6c00
		accent:      lipgloss.Color("208"),
		rowSelected: lipgloss.Color("58"),
		good:        lipgloss.Color("184"), // yellow
		warn:        lipgloss.Color("214"), // orange
		bad:         lipgloss.Color("202"), // red-orange
		mainnet:     lipgloss.Color("184"),
		testnet:     lipgloss.Color("214"),
	},
}

// Live colours, assigned by applyPalette. A few render paths read these
// directly rather than through a style (BorderForeground on modal boxes, the
// selected-row background), so they have to stay in sync with the styles.
var (
	colorBorder      lipgloss.Color
	colorMuted       lipgloss.Color
	colorLabel       lipgloss.Color
	colorValue       lipgloss.Color
	colorGood        lipgloss.Color
	colorWarn        lipgloss.Color
	colorBad         lipgloss.Color
	colorMainnet     lipgloss.Color
	colorTestnet     lipgloss.Color
	colorAccent      lipgloss.Color
	colorRowSelected lipgloss.Color
)

// Styles built from the live colours. These are assigned by buildStyles, NOT
// at declaration: a style captures its colour by value, so one built at
// declaration time would keep the first scheme's colours forever.
//
// Anything here that captures a colour must be (re)built in buildStyles.
// Colour-free styles live in the plain var block further down.
var (
	// styleBorder is the rounded-corner box used for every panel on the
	// dashboard. Padding(0, 1) inserts one column of horizontal breathing
	// room inside the border on each side.
	styleBorder lipgloss.Style

	// styleBorderFocused is the same rounded box but painted with the
	// accent colour so the user can tell at a glance which panel arrow
	// keys will operate on.
	styleBorderFocused lipgloss.Style

	styleLabel  lipgloss.Style
	styleValue  lipgloss.Style
	styleMuted  lipgloss.Style
	styleGood   lipgloss.Style
	styleWarn   lipgloss.Style
	styleBad    lipgloss.Style
	styleAccent lipgloss.Style
	styleTitle  lipgloss.Style

	styleMainnetBadge lipgloss.Style
	styleTestnetBadge lipgloss.Style

	styleStatLabelA lipgloss.Style
	styleStatLabelB lipgloss.Style

	styleTxStatusCol lipgloss.Style

	// Poll list columns. Title has no fixed width — it flexes to fill whatever
	// these three fixed columns leave (see renderPollRow), so the row spans the
	// full damn panel and the title gets the most room. The stat column holds
	// either the cheap "N votes" count or, once the lazy tally lands, the "62% Yes"
	// participation + leading answer.
	stylePollWeightCol lipgloss.Style
	stylePollStatCol   lipgloss.Style
	stylePollTimeCol   lipgloss.Style

	// txKindStyle maps the status enum defined in format.go to the lipgloss
	// colour we want its icon rendered in. Package-level map so renderTxRow
	// doesn't build one on each frame.
	txKindStyle map[TxStatusKind]lipgloss.Style

	configLabelStyle   lipgloss.Style
	configLabelFocused lipgloss.Style
	configValueFocused lipgloss.Style
)

// Styles with no colour of their own — layout only, so they are scheme
// independent and safe to build once at declaration.
var (
	styleStatValueA = lipgloss.NewStyle().Width(22)

	styleTxAmountCol = lipgloss.NewStyle().Width(18).Align(lipgloss.Right)
	styleTxAddrCol   = lipgloss.NewStyle().Width(16)
	styleTxTimeCol   = lipgloss.NewStyle().Width(12)
)

func init() { applyScheme(defaultScheme) }

const (
	defaultScheme = "default"
	testnetScheme = "orange"
)

// applyScheme repaints everything from the named scheme, falling back to the
// default if the name is unknown so a bad name degrades to a plain UI instead
// of a blank one.
func applyScheme(name string) {
	p, ok := schemes[name]
	if !ok {
		p = schemes[defaultScheme]
	}
	applyPalette(p)
}

// applyNetworkPalette selects the scheme for the network we're pointed at.
// Colour is what makes a testnet window recognisable at a glance among
// mainnet ones in a row of tmux panes.
func applyNetworkPalette(testnet bool) {
	if testnet {
		applyScheme(testnetScheme)
		return
	}
	applyScheme(defaultScheme)
}

// applyPalette publishes a palette to the live colours and rebuilds every
// style from them. Safe to call repeatedly and in any order — it assigns all
// state unconditionally rather than mutating in place, so the config modal can
// toggle schemes at runtime without a restart.
func applyPalette(p palette) {
	colorBorder = p.border
	colorMuted = p.muted
	colorLabel = p.label
	colorValue = p.value
	colorAccent = p.accent
	colorRowSelected = p.rowSelected
	colorGood = p.good
	colorWarn = p.warn
	colorBad = p.bad
	colorMainnet = p.mainnet
	colorTestnet = p.testnet
	buildStyles(p)
}

// buildStyles rebuilds every style that captures a colour. Adding a coloured
// style means adding it here too, otherwise it silently keeps whichever
// scheme happened to be active when it was first built.
func buildStyles(p palette) {
	styleBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(0, 1)
	styleBorderFocused = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.accent).
		Padding(0, 1)

	styleLabel = lipgloss.NewStyle().Foreground(p.label)
	styleValue = lipgloss.NewStyle().Foreground(p.value).Bold(true)
	styleMuted = lipgloss.NewStyle().Foreground(p.muted)
	styleGood = lipgloss.NewStyle().Foreground(p.good)
	styleWarn = lipgloss.NewStyle().Foreground(p.warn)
	styleBad = lipgloss.NewStyle().Foreground(p.bad)
	styleAccent = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	styleTitle = lipgloss.NewStyle().Foreground(p.title).Bold(true)

	styleMainnetBadge = lipgloss.NewStyle().Foreground(p.mainnet).Bold(true)
	styleTestnetBadge = lipgloss.NewStyle().Foreground(p.testnet).Bold(true)

	// 15 so the longest labels ("Immature Stake", "Pending Reward", both 14
	// chars) keep a separating space before the value column.
	styleStatLabelA = styleLabel.Width(15)
	styleStatLabelB = styleLabel.Width(12)

	styleTxStatusCol = lipgloss.NewStyle().Width(10).Foreground(p.label)

	stylePollWeightCol = lipgloss.NewStyle().Width(6).Foreground(p.muted)
	stylePollStatCol = lipgloss.NewStyle().Width(22).Foreground(p.muted)
	stylePollTimeCol = lipgloss.NewStyle().Width(8).Align(lipgloss.Right).Foreground(p.muted)

	txKindStyle = map[TxStatusKind]lipgloss.Style{
		TxStatusUpcoming:  styleWarn,
		TxStatusIncoming:  styleAccent,
		TxStatusSending:   styleAccent,
		TxStatusConfirmed: styleGood,
		TxStatusStake:     styleAccent,
	}

	configLabelStyle = styleLabel.Width(12)
	configLabelFocused = styleAccent.Width(12)
	configValueFocused = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
}

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
	case modeHelp:
		return m.renderHelpModal()
	case modePolls:
		return m.renderPollsScreen()
	case modePollDetail:
		return m.renderPollDetailModal()
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
	if m.peersLoaded {
		// Zero peers means the node is effectively offline — render just
		// "peers 0" as a warning instead of a pointless (0↓/0↑) split.
		peers := styleWarn.Render("peers 0")
		if m.peersTotal > 0 {
			peers = styleMuted.Render(fmt.Sprintf("peers %d (%d↓/%d↑)",
				m.peersTotal, m.peersIn, m.peersOut))
		}
		if blockInfo != "" {
			blockInfo = blockInfo + styleMuted.Render(" · ") + peers
		} else {
			blockInfo = peers
		}
	}

	// Right side always shows the running build version. When a newer release is
	// out, a compact green badge advertises the "u" key without duplicating the
	// latest version number shown in the update modal.
	rightParts := []string{}
	if m.updateAvailable && m.latestVersion != "" {
		rightParts = append(rightParts, styleGood.Render("⬆"))
	}
	rightParts = append(rightParts, styleMuted.Render(displayVersion()))
	if blockInfo != "" {
		rightParts = append(rightParts, blockInfo)
	}
	rightSide := strings.Join(rightParts, "  ")

	leftHalf := lipgloss.JoinHorizontal(lipgloss.Top, title, "  ", networkBadge)
	gap := m.width - lipgloss.Width(leftHalf) - lipgloss.Width(rightSide) - 4
	if gap < 1 {
		gap = 1
	}
	line := lipgloss.JoinHorizontal(lipgloss.Top,
		leftHalf,
		strings.Repeat(" ", gap),
		rightSide,
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
	// reveals "this wallet crunches" in anonymous mode — that's within the
	// mode's contract (it hides monetary amounts, not identity).
	if m.staking.Magnitude != nil {
		pending := 0.0
		if m.staking.PendingReward != nil {
			pending = *m.staking.PendingReward
		}
		rows = append(rows, statRow("Pending Reward", fmtBal(pending),
			"Magnitude", styleValue.Render(fmt.Sprintf("%.2f", *m.staking.Magnitude))))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, rows...)
	// The daemon controls rpcError.Message, so every error string that came
	// over RPC goes through sanitizeTerminal like any other daemon field.
	// Same treatment at every error line below.
	if m.walletErr != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, content, "", styleBad.Render("error: "+sanitizeTerminal(m.walletErr)))
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
		return styleWarn.Render("○ " + sanitizeTerminal(m.staking.MiningError))
	}
	return styleMuted.Render("○ no")
}

// researcherBadge says whether this wallet crunches for BOINC or is
// investor-only (issue #7). The CPID is truncated with the same helper the
// address columns use, which keeps the Total row inside 80 columns — a full
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
		return styleMuted.Render("investor")
	default:
		return styleGood.Render("cruncher ") + styleValue.Render(ShortAddress(m.staking.CPID))
	}
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
func (m Model) addrMaxScroll(addrs []ReceivedAddress, rowWidth int) int {
	widest := 0
	for _, a := range addrs {
		if w := segmentsWidth(addressRowSegments(a, m.anonymous, m.ownership(a.Address))); w > widest {
			widest = w
		}
	}
	if max := widest - rowWidth; max > 0 {
		return max
	}
	return 0
}

// renderAddrTabs builds the Mine | Others | All tab bar shown as the panel's
// header line. Each segment is prefixed with its 1/2/3 hotkey so the binding is
// self-documenting (e.g. "1.Mine 12"). The active tab is bracketed and
// accented; inactive tabs are muted and padded with spaces so the bar's width
// doesn't jump when the selection moves. Counts come from addrTabCounts.
func (m Model) renderAddrTabs() string {
	mine, others, all := m.addrTabCounts()
	seg := func(tab addrTab, key, label string, n int) string {
		text := fmt.Sprintf("%s.%s (%d)", key, label, n)
		if m.addrTab == tab {
			return styleAccent.Render("[" + text + "]")
		}
		return styleMuted.Render(" " + text + " ")
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		seg(addrTabMine, "1", "Mine", mine), " ",
		seg(addrTabOthers, "2", "Others", others), " ",
		seg(addrTabAll, "3", "All", all),
	)
}

// renderAddresses draws the scrollable My Addresses panel. Like
// renderTxList, it derives the visible window from the cursor each
// frame. The panel renders a focus indicator (accent border + ▸ on the
// selected row) only when m.focusedArea == focusAddr. Rows are drawn from the
// active tab's slice (see visibleAddresses), with the tab bar as the header.
func (m Model) renderAddresses(maxHeight int) string {
	border := styleBorder
	if m.focusedArea == focusAddr {
		border = styleBorderFocused
	}
	box := border.Width(m.width - 2)
	// Once the user has manually resized the panel, hold that height and pad
	// with blank rows (same as the Transactions box) so it stays a consistent
	// size when switching tabs instead of snapping to each tab's row count. In
	// auto mode we leave the box content-sized so a near-empty panel yields its
	// slack to Transactions.
	if m.addrPanelRows > 0 {
		box = box.Height(maxHeight - 2)
	}

	title := "My Addresses"
	if !m.addrsLoaded {
		return box.Render(styleTitle.Render(title) + "\n" + styleMuted.Render("loading…"))
	}
	if m.addrsErr != "" {
		return box.Render(styleTitle.Render(title) + "\n" + styleBad.Render("error: "+sanitizeTerminal(m.addrsErr)))
	}
	if len(m.addresses) == 0 {
		return box.Render(styleTitle.Render(title) + "\n" + styleMuted.Render("wallet has no addresses yet, run `getnewaddress`"))
	}

	// The tab bar is always rendered, even when the active tab is empty, so the
	// user can switch away from a tab that filtered everything out.
	visible := m.visibleAddresses()
	if len(visible) == 0 {
		return box.Render(m.renderAddrTabs() + "\n" + styleMuted.Render("no addresses in this tab"))
	}

	// Available data rows inside the box: maxHeight - 2 (borders) - 1 (tab bar).
	maxRows := maxHeight - 3
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
	header := m.renderAddrTabs()
	if len(visible) > maxRows {
		header += styleMuted.Render(fmt.Sprintf("  %d/%d", m.addrCursor+1, len(visible)))
	}
	if m.focusedArea == focusAddr && maxScroll > 0 {
		header += styleMuted.Render("  ←/→")
	}
	lines := []string{header}

	end := offset + maxRows
	if end > len(visible) {
		end = len(visible)
	}
	for i := offset; i < end; i++ {
		prefix := "  "
		row := clipSegments(addressRowSegments(visible[i], m.anonymous, m.ownership(visible[i].Address)), hoff, rowWidth)
		if i == m.addrCursor && m.focusedArea == focusAddr {
			// Carry the highlight background through the cursor marker and
			// across the whole row, so it's coloured edge to edge.
			prefix = styleAccent.Background(colorRowSelected).Render("▸ ")
			row = fillBackground(row, rowWidth)
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
func addressRowSegments(a ReceivedAddress, anonymous bool, own addrOwnership) []styledSeg {
	// Sanitize the daemon-sourced texts as the segments are built: every
	// consumer (addrMaxScroll's widest-row measurement, clipSegments'
	// column slicing) measures these exact strings, so cleaning them any
	// later would make the horizontal-scroll math disagree with what is
	// actually printed.
	segs := []styledSeg{{sanitizeTerminal(a.Address), styleValue}}
	gap := styledSeg{"  ", styleMuted}
	if own == ownForeign {
		// listreceivedbyaddress includes addresses you've only labelled but
		// don't own; flag them in red (a stronger cue than watch-only's
		// orange) so a foreign address is never mistaken for one of yours.
		segs = append(segs, gap, styledSeg{"⚠ not yours", styleBad})
	}
	if a.InvolvesWatchonly {
		// The eye glyph hints at the meaning visually; the trailing word
		// makes it explicit on terminals that fall back to a tofu box.
		// styleWarn (orange) is the same shade used for "wallet locked"
		// in the stats panel, both convey "this needs attention before
		// you try to sign or spend".
		segs = append(segs, gap, styledSeg{"👁 watch-only", styleWarn})
	}
	if l := sanitizeTerminal(a.DisplayLabel()); l != "" {
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

// fillBackground paints the selection background across an already-rendered
// line and pads it to width columns, so a highlighted row is coloured edge to
// edge. We can't just wrap the line in a background style: every style reset
// inside it (lipgloss ends each coloured run with one) would clear the
// background, leaving only the start tinted — which is the bug this fixes.
// Instead we re-assert the background escape immediately after every reset and
// fill the remainder with background spaces. The escape sequences are derived
// from lipgloss itself (via a NUL-delimited probe) so they match whatever
// colour profile is active; on a no-colour terminal the probe yields no escape
// and the line passes through unchanged.
func fillBackground(line string, width int) string {
	probe := lipgloss.NewStyle().Background(colorRowSelected).Render("\x00")
	i := strings.IndexByte(probe, 0)
	if i <= 0 {
		return line // no-colour profile: nothing to paint
	}
	open, reset := probe[:i], probe[i+1:]

	body := open + strings.ReplaceAll(line, reset, reset+open)
	if pad := width - lipgloss.Width(line); pad > 0 {
		body += strings.Repeat(" ", pad)
	}
	return body + reset
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

// truncate shortens text to at most maxCols display columns, appending an
// ellipsis when it had to cut. Width-aware (wide glyphs count as 2) so it
// never overflows a fixed-width column. maxCols <= 0 returns "".
func truncate(text string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	if runewidth.StringWidth(text) <= maxCols {
		return text
	}
	return runewidth.Truncate(text, maxCols, "…")
}

// renderBar draws a fixed-width proportional bar (filled █ + empty ░) for a
// fraction in [0,1] — used by the poll detail popup's per-choice results
// breakdown. Out-of-range fractions are clamped.
func renderBar(fraction float64, width int) string {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return styleAccent.Render(strings.Repeat("█", filled)) + styleMuted.Render(strings.Repeat("░", width-filled))
}

// listWindow computes the visible-row budget and scroll offset for a bordered
// scroll panel that is `height` rows tall and holds `total` rows. The chrome is
// 3 rows (2 border + 1 title), and the offset slides forward only — starting at
// 0 and advancing just enough to keep the cursor on the last visible row.
// Shared by renderTxList and renderPollsList so their scroll math can't drift.
func listWindow(height, cursor, total int) (maxRows, offset int) {
	maxRows = height - 3
	if maxRows < 1 {
		maxRows = 1
	}
	if maxRows > total {
		maxRows = total
	}
	if cursor >= maxRows {
		offset = cursor - maxRows + 1
	}
	return maxRows, offset
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
		return boxStyle.Render(title + "\n" + styleBad.Render("error: "+sanitizeTerminal(m.txsErr)))
	}
	if len(m.txs) == 0 {
		return boxStyle.Render(title + "\n" + styleMuted.Render("no transactions yet"))
	}

	maxRows, offset := listWindow(height, m.txCursor, len(m.txs))
	lines := []string{title}
	for i := offset; i < offset+maxRows && i < len(m.txs); i++ {
		prefix := "  "
		// A missing cache entry yields "", the same value as a lookup that
		// came back with no type. The row renders both as a generic
		// "(contract)", which reads correctly either way: still resolving,
		// or a contract the daemon itself could not classify. Only the
		// detail modal needs to tell the two apart.
		line := renderTxRow(m.txs[i], m.anonymous, m.txContracts[m.txs[i].TxID])
		if i == m.txCursor && m.focusedArea == focusTx {
			// Highlight only the focused panel's cursor row. An unfocused
			// tx list leaves the cursor as a silent bookmark, symmetric
			// with the addresses panel so the two behave the same. Paint the
			// whole row via fillBackground (the same edge-to-edge highlight
			// the addresses panel uses).
			prefix = styleAccent.Background(colorRowSelected).Render("▸ ")
			line = fillBackground(line, m.panelRowWidth())
		}
		lines = append(lines, prefix+line)
	}
	return boxStyle.Render(strings.Join(lines, "\n"))
}

// renderTxRow renders one transaction line. contractType is the cached
// Gridcoin contract kind for this txid ("beacon", "vote", …), or "" when it
// is unknown or the transaction carries no contract; see Model.txContracts.
func renderTxRow(tx Transaction, anonymous bool, contractType string) string {
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
	} else if IsContractCandidate(tx) {
		// A beacon or vote has no counterparty address, so this column would
		// otherwise be blank and the row would read as broken data. Name the
		// contract instead. Every label stays under ShortAddress's 12-char
		// eliding threshold — "(sidestake)", the longest of the daemon's
		// contract types, is 11 — so these pass through it unaltered.
		if contractType == "" {
			contractType = "contract"
		}
		addr = "(" + contractType + ")"
	}
	// Sanitized after the label is assembled, so the daemon-supplied contract
	// type is covered along with tx.Address, and before ShortAddress so its
	// length check counts the characters that will actually be printed.
	addrCol := styleTxAddrCol.Render(ShortAddress(sanitizeTerminal(addr)))

	timeCol := styleTxTimeCol.Render(styleMuted.Render(FormatRelativeTime(tx.Time)))
	catCol := styleMuted.Render(sanitizeTerminal(tx.Category))

	return lipgloss.JoinHorizontal(lipgloss.Top,
		icon, " ", status, amountCol, "  ", addrCol, "  ", timeCol, "  ", catCol,
	)
}

// renderPollsScreen is the full-screen governance polls list (mode "p"). It
// reuses the dashboard header (network badge + block height) so the chrome
// matches, fills the middle with the scrollable poll list, and pins a
// polls-specific key legend at the bottom.
func (m Model) renderPollsScreen() string {
	header := m.renderHeader()
	footer := m.renderPollsFooter()
	available := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if available < 3 {
		available = 3
	}
	body := m.renderPollsList(available)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// renderPollsList draws the scrollable poll rows. Same cursor-window scroll
// math as renderTxList: the offset is derived fresh each frame from pollCursor
// so nothing needs storing on the by-value Model.
func (m Model) renderPollsList(height int) string {
	boxStyle := styleBorderFocused.Width(m.width - 2).Height(height - 2)
	scope := "all"
	if !m.pollsShowFinished {
		scope = "active"
	}
	title := styleTitle.Render("Polls") + "  " + styleMuted.Render(scope)

	// Loading / error / empty all render as the title plus one status line.
	var status string
	switch {
	case !m.pollsLoaded:
		status = styleMuted.Render("loading…")
	case m.pollsErr != "":
		status = styleBad.Render("error: " + sanitizeTerminal(m.pollsErr))
	case len(m.polls) == 0:
		status = styleMuted.Render("no polls")
	}
	if status != "" {
		return boxStyle.Render(title + "\n" + status)
	}

	maxRows, offset := listWindow(height, m.pollCursor, len(m.polls))
	lines := []string{title}
	for i := offset; i < offset+maxRows && i < len(m.polls); i++ {
		prefix := "  "
		line := m.renderPollRow(m.polls[i])
		if i == m.pollCursor {
			prefix = styleAccent.Background(colorRowSelected).Render("▸ ")
			line = fillBackground(line, m.panelRowWidth())
		}
		lines = append(lines, prefix+line)
	}
	return boxStyle.Render(strings.Join(lines, "\n"))
}

// renderPollRow renders one poll line: status dot · title · weight-type ·
// stat · time-left. The stat column shows the lazily-fetched tally
// ("62% Yes") once it's cached, otherwise the cheap "N votes" count from
// listpolls.
func (m Model) renderPollRow(p Poll) string {
	// Parse the expiration once and derive both the status dot and the
	// time-left column from it (both PollExpired and FormatPollTimeLeft would
	// otherwise re-parse the same string every frame).
	exp := ParsePollTime(p.Expiration)
	dot := styleGood.Render("●")
	if pollExpired(exp) {
		dot = styleMuted.Render("○")
	}

	// Flex the title to fill the row: panel width minus the dot (1), its
	// trailing space (1), the two-space gap before the stat column, and the
	// three fixed columns. GetWidth keeps this correct if those widths change.
	fixed := 2 + stylePollWeightCol.GetWidth() + 2 + stylePollStatCol.GetWidth() + stylePollTimeCol.GetWidth()
	titleWidth := m.panelRowWidth() - fixed
	if titleWidth < 12 {
		titleWidth = 12
	}
	// Poll title, weight type and leading choice are on-chain data any network
	// participant can author (see sanitizeTerminal), cleaned before truncate /
	// ShortWeightType so the width budget matches the printed text.
	title := lipgloss.NewStyle().Width(titleWidth).Render(truncate(sanitizeTerminal(p.Title), titleWidth-1))
	weight := stylePollWeightCol.Render(ShortWeightType(sanitizeTerminal(p.WeightType)))

	var stat string
	if r, ok := m.pollResults[p.ID]; ok {
		pct := "—"
		if r.VotePercentAVW != nil {
			pct = fmt.Sprintf("%.0f%%", *r.VotePercentAVW)
		}
		leader := sanitizeTerminal(r.TopChoice)
		if leader == "" {
			leader = "—"
		}
		stat = fmt.Sprintf("%-4s %s", pct, leader)
	} else {
		stat = fmt.Sprintf("%d votes", p.Votes)
	}
	statCol := stylePollStatCol.Render(truncate(stat, 21))

	timeCol := stylePollTimeCol.Render(formatPollTimeLeft(exp))

	return lipgloss.JoinHorizontal(lipgloss.Top, dot, " ", title, weight, "  ", statCol, timeCol)
}

// renderPollDetailModal is the centered popup opened with enter on a selected
// poll. It shows the full poll metadata plus, from the lazily-fetched
// getpollresults tally cached in m.pollResults, a per-choice results breakdown
// (each option's share of the total weight as a bar). If the tally is still in
// flight the results section shows "tallying…" and fills in when it lands.
func (m Model) renderPollDetailModal() string {
	p := m.selectedPoll()
	if p == nil {
		// Selection vanished (list reloaded to empty); fall back to the list.
		return m.renderPollsScreen()
	}

	field := func(label, value string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			styleLabel.Width(14).Render(label),
			styleValue.Render(value),
		)
	}
	orDash := func(s string) string {
		if s == "" {
			return "—"
		}
		return s
	}

	exp := ParsePollTime(p.Expiration)
	var status string
	if pollExpired(exp) {
		status = styleMuted.Render("○ ended")
	} else {
		status = styleGood.Render("● active") + styleMuted.Render(" · "+formatPollTimeLeft(exp)+" left")
	}

	// The daemon-sourced values are sanitized at the call sites, not inside
	// field: field also receives values we already rendered ourselves (the
	// Status line above, and its counterpart in renderTxDetailModal), and
	// sanitizing those would strip our own SGR colour escapes along with the
	// hostile ones. Everything here is on-chain poll-author data, cleaned
	// before truncate so the column budget matches the printed text.
	lines := []string{
		styleTitle.Render("Poll"),
		"",
		field("Title", sanitizeTerminal(p.Title)),
		field("Status", status),
		field("Question", orDash(truncate(sanitizeTerminal(p.Question), 74))),
		field("URL", orDash(truncate(sanitizeTerminal(p.URL), 74))),
		field("Weight type", orDash(sanitizeTerminal(p.WeightType))),
		field("Responses", orDash(sanitizeTerminal(p.ResponseType))),
		field("Created", orDash(sanitizeTerminal(p.Timestamp))),
		field("Duration", fmt.Sprintf("%d days", p.DurationDays)),
		field("Votes", fmt.Sprintf("%d", p.Votes)),
	}

	r, ok := m.pollResults[p.ID]
	if ok && r.VotePercentAVW != nil {
		lines = append(lines, field("Participation", fmt.Sprintf("%.1f%% AVW", *r.VotePercentAVW)))
	}

	lines = append(lines, "", styleTitle.Render("Results"))
	switch {
	case ok && len(r.Responses) == 0:
		lines = append(lines, styleMuted.Render("  no votes yet"))
	case !ok && m.pollResultErr[p.ID] != "":
		// The tally finished with an error (e.g. a transient reorg per the
		// daemon's getpollresults note). Show it instead of a stuck spinner.
		lines = append(lines,
			styleBad.Render("  couldn't load results: "+sanitizeTerminal(m.pollResultErr[p.ID])),
			styleMuted.Render("  press r to retry"))
	case !ok:
		// Pending, or the brief window just after opening: animate so it's
		// clearly still working, not frozen.
		lines = append(lines, styleMuted.Render("  tallying… "+spinnerFrames[m.spinnerFrame]))
	default:
		// Each response is two lines: the full choice text (poll answers are
		// often whole sentences, so truncating them into a column would hide the
		// point), then an indented stats line — a share bar plus labelled
		// numbers, so it's clear what each figure means without a column header.
		for _, resp := range r.Responses {
			frac := 0.0
			if r.TotalWeight > 0 {
				frac = resp.Weight / r.TotalWeight
			}
			stats := fmt.Sprintf("%.0f%% share · %s weight · %s votes",
				frac*100, formatCompactNumber(resp.Weight), formatVoteCount(resp.Votes))
			lines = append(lines,
				"  "+styleValue.Render(sanitizeTerminal(resp.Choice)),
				lipgloss.JoinHorizontal(lipgloss.Top, "    ", renderBar(frac, 16), "  ", styleMuted.Render(stats)),
			)
		}
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

// renderStatusBar renders one bordered full-width bar: the key legend on the
// left, and the refresh spinner pinned to the right while any RPC fetch is in
// flight. Shared by the dashboard footer and the polls-screen footer so their
// width/gap budget can't drift.
func (m Model) renderStatusBar(keys []string) string {
	left := styleMuted.Render(strings.Join(keys, "  "))
	right := ""
	if m.inflight > 0 {
		right = styleAccent.Render(spinnerFrames[m.spinnerFrame])
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if gap < 1 {
		gap = 1
	}
	line := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
	return styleBorder.Width(m.width - 2).Render(line)
}

// renderPollsFooter is the key legend for the polls screen: scope toggle,
// scrolling, refresh, and back.
func (m Model) renderPollsFooter() string {
	scopeKey := "[tab] show active"
	if !m.pollsShowFinished {
		scopeKey = "[tab] show all"
	}
	return m.renderStatusBar([]string{
		scopeKey,
		"[enter] details",
		"[↑/↓ · pgup/pgdn] scroll",
		"[r]efresh",
		"[esc] back",
	})
}

func (m Model) renderFooter() string {
	anonLabel := "[a]non"
	if m.anonymous {
		anonLabel = "[a]non ●"
	}
	keys := []string{"[?]help", "[s]end", "sign [m]sg"}
	// [e]dit label only acts on the focused addresses panel, so surface it
	// contextually rather than implying it works everywhere. (The 1/2/3 tab
	// keys are self-documented in the panel's own tab bar.)
	if m.focusedArea == focusAddr {
		keys = append(keys, "[e]dit label")
	}
	keys = append(keys,
		"[p]olls",
		"[c]onfig",
		"[u]pdate",
		"[r]efresh",
		anonLabel,
		"[tab] switch panel",
		"[↑/↓ · pgup/pgdn] scroll",
		"[+/-] resize",
		"[q]uit",
	)
	// The right side shows a spinning Braille dot while any RPC fetch is in
	// flight so the user can see the TUI is alive and talking to the daemon;
	// when all fetches settle it goes blank — a brief flash every refresh
	// interval rather than a persistent clock.
	return m.renderStatusBar(keys)
}

func (m Model) renderSendModal() string {
	var body string
	switch m.send.step {
	case sendStepAddress:
		body = "Recipient address:\n\n" + m.send.address.View()
		if m.send.validating {
			body += "\n\n" + styleMuted.Render("validating…")
		} else if m.send.errMsg != "" {
			// errMsg carries the validateaddress RPC error verbatim on this
			// step (and unlock/send errors elsewhere), so it's daemon text
			// like any other — sanitized at every render below.
			body += "\n\n" + styleBad.Render(sanitizeTerminal(m.send.errMsg))
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
			body += "\n\n" + styleBad.Render(sanitizeTerminal(m.send.errMsg))
		} else {
			body += "\n\n" + styleMuted.Render("enter to continue · backspace to go back · esc to cancel")
		}
	case sendStepPassphrase:
		body = "Wallet is locked. Passphrase:\n\n" + m.send.passphrase.View()
		if m.send.errMsg != "" {
			body += "\n\n" + styleBad.Render(sanitizeTerminal(m.send.errMsg))
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
			body = styleBad.Render("send failed") + "\n\n" + sanitizeTerminal(m.send.resultErr)
		} else {
			// The txid is the daemon's response string too, sanitized like
			// the error branch above.
			body = styleGood.Render("sent ✓") + "\n\n" + "txid: " + sanitizeTerminal(m.send.resultTxID)
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
			// The signmessage / unlock RPC error, daemon-authored text —
			// sanitized like every other daemon string.
			body = styleBad.Render("sign failed") + "\n\n" + sanitizeTerminal(m.sign.resultErr)
		} else {
			// The signature is a daemon response string; a legitimate one is
			// pure base64, so sanitizing is a no-op unless something hostile
			// snuck in. The message is the user's own typed input, left as-is.
			body = styleGood.Render("signed ✓") + "\n\n" +
				styleLabel.Render("Message:") + "\n" + m.sign.message.Value() + "\n\n" +
				styleLabel.Render("Signature (base64):") + "\n" + sanitizeTerminal(m.sign.resultSig)
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
	// Same daemon-sourced address string the panel shows (see
	// addressRowSegments), so it gets the same scrubbing on this surface.
	header := styleTitle.Render("Edit label") + "\n" +
		styleLabel.Render("Address: ") + styleAccent.Render(sanitizeTerminal(m.edit.address))

	body := "Label:\n\n" + m.edit.label.View()
	// Heads-up for the setaccount quirk (see runSetLabel): relabeling an
	// address that is its account's current receiving address makes
	// gridcoinresearchd spawn a replacement carrying the old label. We can't
	// suppress it (setaccount is the only label RPC Gridcoin exposes), so we
	// warn rather than surprise. The modal's Width wraps this for us.
	body += "\n\n" + styleMuted.Render("Heads-up: the daemon may spawn an extra address with the old label on save. harmless quirk, coins unaffected.")
	if m.edit.errMsg != "" {
		// Carries the setaccount RPC error verbatim on failure, so it gets
		// the same sanitizing as every other daemon-authored string.
		body += "\n\n" + styleBad.Render(sanitizeTerminal(m.edit.errMsg))
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

	// Daemon-sourced values are sanitized at each call site rather than
	// inside field: field also receives values we rendered ourselves (the
	// Status line above, the muted "resolving…" below), and sanitizing those
	// would strip our own SGR colour escapes along with the hostile ones.
	// The IsContractCandidate branches below test tx.Address BEFORE
	// sanitizing, so a category/address forged out of control bytes can't
	// flip a real payment into the address-less contract branch.
	addr := sanitizeTerminal(tx.Address)
	if tx.Address == "" && (tx.Category == "generate" || tx.Category == "immature") {
		addr = "(stake reward, no counterparty address)"
	} else if IsContractCandidate(tx) {
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
		field("Category", sanitizeTerminal(tx.Category)),
	}
	// What this "send" actually was. The daemon's own category can't say —
	// the contract is only visible via gettransaction, normally already
	// fetched by the txsMsg batch and otherwise asked for when this modal
	// opens (see update.go, case "enter"). Three states: a known type,
	// still-resolving, and resolved-as-no-contract, which drops the line.
	if IsContractCandidate(tx) {
		if ctype, ok := m.txContracts[tx.TxID]; !ok {
			lines = append(lines, field("Contract", styleMuted.Render("resolving…")))
		} else if ctype != "" {
			lines = append(lines, field("Contract", sanitizeTerminal(ctype)))
		}
	}
	lines = append(lines, field("Amount", amountStr))
	if tx.Fee != 0 {
		lines = append(lines, field("Fee", feeStr))
	}
	lines = append(lines,
		field("Address", addr),
		field("TxID", sanitizeTerminal(tx.TxID)),
		field("Confirmations", confLine),
		field("Time", timeLine),
	)
	if tx.BlockHash != "" {
		lines = append(lines, field("Block hash", sanitizeTerminal(tx.BlockHash)))
	}
	if tx.Comment != "" {
		lines = append(lines, field("Comment", sanitizeTerminal(tx.Comment)))
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

// renderHelpModal is the read-only cheat sheet opened with "?". It lists every
// key grouped by what it does, with a short plain-language note on each, plus a
// one-line summary of what the dashboard is. Any key closes it (handled in
// update.go::handleKey, case modeHelp).
// displayVersion is the build version for the UI, with "dev" spelled out so a
// local build reads clearly rather than showing a bare "dev".
func displayVersion() string {
	if version == "dev" {
		return "dev build"
	}
	return "v" + version
}

// clampChangelog trims an over-long changelog so the modal keeps a sane height
// and the confirm keys never get pushed off screen. A single version's notes
// are normally a few lines; this only bites on an unusually chatty release.
func clampChangelog(s string) string {
	const maxLines = 12
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], styleMuted.Render("… (see the release page for the rest)"))
	}
	return strings.Join(lines, "\n")
}

func renderReleaseChangelogs(releases []releaseInfo) string {
	if len(releases) == 0 {
		return styleMuted.Render("(no release notes)")
	}
	sections := make([]string, 0, len(releases))
	for _, rel := range releases {
		notes := trimStampSection(rel.Body)
		if notes == "" {
			notes = styleMuted.Render("(no release notes)")
		}
		version := "v" + strings.TrimPrefix(rel.TagName, "v")
		sections = append(sections, styleTitle.Render(version)+"\n"+notes)
	}
	return clampChangelog(strings.Join(sections, "\n\n"))
}

// renderUpdateModal draws the self-update flow: a live check, then either
// "up to date" or a changelog + confirm, then an install progress line. The
// changelog is the original release notes with the on-chain stamp section
// stripped (see trimStampSection).
func (m Model) renderUpdateModal() string {
	var body string
	switch m.update.step {
	case updateStepChecking:
		body = styleMuted.Render("Checking GitHub for a newer release…")
	case updateStepUpToDate:
		body = styleGood.Render("✓ You're on the latest version") + "\n\n" +
			styleMuted.Render("Current: "+displayVersion()) + "\n\n" +
			styleMuted.Render("[esc] close")
	case updateStepAvailable:
		latest := "v" + strings.TrimPrefix(m.update.rel.TagName, "v")
		body = "Current " + displayVersion() + "  →  " + styleGood.Render(latest) + "\n\n"
		body += styleTitle.Render("What changed") + "\n" + renderReleaseChangelogs(m.update.missedReleases) + "\n\n"
		body += styleWarn.Render("Downloads and replaces the binary, then restarts the TUI.") + "\n\n"
		body += styleMuted.Render("[y] Update & restart   [n] Cancel")
	case updateStepInstalling:
		body = styleMuted.Render("Downloading and installing…") + "\n\n" +
			styleMuted.Render("The TUI will restart automatically when it's done.")
	case updateStepFailed:
		body = styleBad.Render("Update failed") + "\n\n" + m.update.errMsg + "\n\n" +
			styleMuted.Render("[esc] close")
	}

	modalWidth := 64
	if max := m.width - 4; modalWidth > max && max > 0 {
		modalWidth = max
	}
	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(modalWidth).
		Render(styleTitle.Render("Updates") + "\n\n" + body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) renderHelpModal() string {
	// keyRow renders one "keys → what they do" line: the keys in the accent
	// colour in a fixed-width column so the descriptions line up.
	keyRow := func(keys, desc string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			styleAccent.Width(12).Render(keys),
			styleLabel.Render(desc),
		)
	}

	lines := []string{
		styleTitle.Render("Help"),
		styleMuted.Render("A read-only view of a running Gridcoin wallet: balance, staking,"),
		styleMuted.Render("lock, block height, your addresses, and recent transactions."),
		styleMuted.Render("You can also send coins, sign a message, or relabel an address."),
		"",
		styleTitle.Render("Move around"),
		keyRow("↑ ↓  k j", "Move the cursor in the focused panel"),
		keyRow("PgUp PgDn", "Jump a page (also Ctrl+U / Ctrl+D)"),
		keyRow("g G", "First / last row (also Home / End)"),
		keyRow("Tab", "Switch focus between the two panels"),
		keyRow("← →  h l", "Slide a too-wide address row sideways"),
		"",
		styleTitle.Render("My Addresses"),
		keyRow("1 2 3", "Show Mine, Others, or All addresses"),
		keyRow("+ −", "Grow or shrink the panel; 0 resets it"),
		keyRow("e", "Rename the selected address (blank clears it)"),
		"",
		styleTitle.Render("Do things"),
		keyRow("Enter", "Open the selected transaction in full"),
		keyRow("s", "Send GRC (checks the address, unlocks only if needed)"),
		keyRow("m", "Sign a message with one of your addresses"),
		keyRow("p", "Browse on-chain governance polls (tab: all / active)"),
		keyRow("c", "Change host, port, login, or refresh for this session"),
		keyRow("u", "Check GitHub for a newer release and update in place"),
		keyRow("a", "Hide every amount on screen, handy when sharing"),
		keyRow("r", "Refresh now instead of waiting for the next poll"),
		keyRow("? q", "This help; q quits (also Ctrl+C)"),
		"",
		styleMuted.Render("press any key to close"),
	}

	modalWidth := 66
	if max := m.width - 4; modalWidth > max && max > 0 {
		modalWidth = max
	}
	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(modalWidth).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
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
