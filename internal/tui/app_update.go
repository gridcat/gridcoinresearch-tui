// This file is the reactive heart of the TUI. Bubble Tea's central loop
// calls our Update method once per incoming message; Update decides how
// the Model should change and what side-effect to run next.
//
// Key Bubble Tea concepts used here:
//
//	tea.Msg:  any value that describes "something happened". Can be a
//	           keystroke (tea.KeyMsg), a window resize (tea.WindowSizeMsg),
//	           a timer firing (tickMsg we define below), or the result of
//	           an RPC call (walletMsg, txsMsg, etc.)
//
//	tea.Cmd:  a function that returns a Msg. Bubble Tea runs Cmds in
//	           goroutines for us, so the TUI never blocks. When the Cmd
//	           returns, its Msg is delivered back to Update.
//
//	Update(msg) returns (Model, Cmd): the new state and a follow-up Cmd
//	           to run (or nil for none). tea.Batch runs several Cmds
//	           concurrently; tea.Tick schedules a Msg for the future.
//
// So a typical cycle looks like:
//  1. tickMsg arrives → Update returns (m, Batch(fetchWallet, fetchTxs,…))
//  2. fetchWallet runs in a goroutine, calls GetWalletInfo, returns
//     walletMsg{w, err}
//  3. Update receives walletMsg, stores m.wallet = w, returns (m, nil)
//  4. View renders the new Model
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// spinnerInterval drives the spinner repaint rate. Each tick triggers a full
// View() pass, and on a laggy view-link (e.g. SSH to a small box) those writes
// can back up and stall Bubble Tea's single event loop. 250 ms keeps the
// animation legible while emitting far fewer frames than a 10 Hz spinner.
const spinnerInterval = 250 * time.Millisecond

// spinnerTickCmd schedules the next spinner frame. The spinner message
// handler checks m.inflight before scheduling another tick, so the
// spinner self-terminates once all fetches settle.
func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(t time.Time) tea.Msg { return spinnerTickMsg(t) })
}

// bumpInflight increments the inflight counter and, if no spinner chain is
// already running, returns a spinnerTickCmd to start one. Callers append the
// returned Cmd (or nil) to their tea.Batch; tea.Batch silently drops nil, so
// passing it unconditionally is safe. Gating on spinnerRunning (rather than
// inflight == 0) means a burst of back-to-back fetches can't each spawn a
// fresh spinner lineage that then outlives the others.
func (m *Model) bumpInflight(n int) tea.Cmd {
	var cmd tea.Cmd
	if !m.spinnerRunning {
		m.spinnerRunning = true
		cmd = spinnerTickCmd()
	}
	m.inflight += n
	return cmd
}

// finishFetch decrements the inflight counter without letting it go
// below zero. Every RPC-result handler calls this exactly once.
func (m *Model) finishFetch() {
	if m.inflight > 0 {
		m.inflight--
	}
}

// ---- Commands ---------------------------------------------------------
//
// A tea.Cmd is literally `func() tea.Msg`. Each helper below returns one.
// They are pure: no side effects on the Model; Bubble Tea runs them in
// goroutines and hands the returned Msg back to Update.

// tickCmd schedules the next polling tick. We re-arm it from the tickMsg
// handler so the timer reschedules itself as long as the program runs.
func (m *Model) tickCmd() tea.Cmd {
	return tea.Tick(m.cfg.Refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// refreshAllCmd fires all six fetches SEQUENTIALLY via tea.Sequence.
//
// tea.Sequence, unlike tea.Batch, runs its child Cmds one at a time and
// waits for each to land its Msg back through Update before starting the
// next. We use it deliberately here so the TUI only holds one
// gridcoinresearchd RPC worker thread (and one wallet lock) at any
// moment. A parallel tea.Batch of 4-5 concurrent RPCs can pin the
// daemon's entire RPC thread pool while slow calls like
// listreceivedbyaddress are running, which starves other RPC clients on
// the same daemon (grcpay, bitcoin-cli, other dashboards). Serialising
// costs us ~a few hundred milliseconds of wall-clock on a healthy
// daemon and prevents the TUI from being a bad neighbour on a shared
// one.
func (m *Model) refreshAllCmd() tea.Cmd {
	return tea.Sequence(
		fetchWallet(m.rpc),
		fetchChain(m.rpc),
		fetchStaking(m.rpc),
		fetchPeers(m.rpc),
		fetchTxs(m.rpc, m.txsLastBlock),
		fetchAddrs(m.rpc),
	)
}

// refreshCoreCmd is the serialised 5-fetch batch used on every timer
// tick. Same rationale as refreshAllCmd (see its comment), but we
// deliberately omit fetchAddrs here because ticks are supposed to be
// lightweight; addresses refresh event-driven from the txsMsg handler
// when a genuinely new tx is detected.
func (m *Model) refreshCoreCmd() tea.Cmd {
	return tea.Sequence(
		fetchWallet(m.rpc),
		fetchChain(m.rpc),
		fetchStaking(m.rpc),
		fetchPeers(m.rpc),
		fetchTxs(m.rpc, m.txsLastBlock),
	)
}

// Init is called once when the program starts. Whatever Cmd it returns is
// the first action the runtime executes. We kick off the recurring tick,
// the initial RPC fetches, and the spinner loop (which will self-stop
// once all six fetches land because NewModel pre-seeded inflight=6).
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.tickCmd(), m.refreshAllCmd(), spinnerTickCmd()}
	if !m.cfg.NoUpdateCheck {
		// Fire one (silent, non-manual) check shortly after launch, and arm the
		// periodic re-check.
		cmds = append(cmds, checkUpdateCmd(false), m.updateTickCmd())
	}
	// Peer sharing rides a heartbeat that is always armed; whether the user
	// opted in, and whether a report is due, is decided on each beat (see
	// sharingTickCmd for why the timer itself is not gated on consent). No
	// first-launch burst either: the first report is a full interval out, so
	// the wallet has time to build a real peer set before anything is sent.
	cmds = append(cmds, m.sharingTickCmd())
	return tea.Batch(cmds...)
}

// Update is the core of the Elm architecture: input message → new state +
// optional follow-up command. Note the value receiver `(m Model)`: each
// call starts with a fresh local copy, we mutate that copy, and return it.
// This is how Bubble Tea's "immutable Model" feel is achieved in Go.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Type switch: msg is a tea.Msg interface, and we branch on its concrete
	// type. Inside each case, `msg` is automatically retyped to that case.
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		// Anti-pileup: if the previous tick's fetches haven't all come
		// back yet (slow daemon, big wallet), skip this tick entirely.
		// We still re-arm the tick timer so we check again in another
		// cfg.Refresh seconds. This stops a slow daemon from
		// accumulating dozens of concurrent RPCs faster than they
		// complete.
		if m.inflight > 0 {
			return m, m.tickCmd()
		}
		// Refresh wallet/chain/staking/peers and tx deltas on every tick,
		// serialised via refreshCoreCmd so we only hold one RPC worker
		// thread at a time.
		spin := m.bumpInflight(5)
		return m, tea.Batch(m.tickCmd(), m.refreshCoreCmd(), spin)

	case spinnerTickMsg:
		// Advance the spinner frame only while something is actually
		// being fetched. Once inflight drops to 0 the chain stops
		// scheduling follow-ups, clears spinnerRunning so the next fetch
		// can start a fresh one, and the footer right-side goes blank.
		if m.inflight == 0 {
			m.spinnerRunning = false
			return m, nil
		}
		m.spinnerFrame = (m.spinnerFrame + 1) % len(ui.SpinnerFrames)
		return m, spinnerTickCmd()

	case walletMsg:
		return m.onWalletMsg(msg)

	case chainMsg:
		return m.onChainMsg(msg)

	case stakingMsg:
		return m.onStakingMsg(msg)

	case peersMsg:
		return m.onPeersMsg(msg)

	case sharingTickMsg:
		return m.onSharingTickMsg(msg)

	case sharingDoneMsg:
		return m.onSharingDoneMsg(msg)

	case txsMsg:
		return m.onTxsMsg(msg)

	case addrsMsg:
		return m.onAddrsMsg(msg)

	case addrMineMsg:
		return m.onAddrMineMsg(msg)

	case txContractsMsg:
		return m.onTxContractsMsg(msg)

	case validateMsg:
		return m.onValidateMsg(msg)

	case sendResultMsg:
		return m.onSendResultMsg(msg)

	case signResultMsg:
		return m.onSignResultMsg(msg)

	case setLabelResultMsg:
		return m.onSetLabelResultMsg(msg)

	case addLabelValidateMsg:
		return m.onAddLabelValidateMsg(msg)

	case addLabelResultMsg:
		return m.onAddLabelResultMsg(msg)

	case pollsMsg:
		return m.onPollsMsg(msg)

	case pollSettleMsg:
		return m.onPollSettleMsg(msg)

	case pollResultMsg:
		return m.onPollResultMsg(msg)

	case updateTickMsg:
		return m.onUpdateTickMsg(msg)

	case updateCheckMsg:
		return m.onUpdateCheckMsg(msg)

	case updateInstallMsg:
		return m.onUpdateInstallMsg(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// ---- Key handling -----------------------------------------------------
//
// Key handling branches by mode first so modal screens get their own
// isolated keybinding scope. The dashboard handler is the "outer" one.
