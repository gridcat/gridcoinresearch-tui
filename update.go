// This file is the reactive heart of the TUI. Bubble Tea's central loop
// calls our Update method once per incoming message; Update decides how
// the Model should change and what side-effect to run next.
//
// Key Bubble Tea concepts used here:
//
//	tea.Msg  — any value that describes "something happened". Can be a
//	           keystroke (tea.KeyMsg), a window resize (tea.WindowSizeMsg),
//	           a timer firing (tickMsg we define below), or the result of
//	           an RPC call (walletMsg, txsMsg, etc.)
//
//	tea.Cmd  — a function that returns a Msg. Bubble Tea runs Cmds in
//	           goroutines for us, so the TUI never blocks. When the Cmd
//	           returns, its Msg is delivered back to Update.
//
//	Update(msg) returns (Model, Cmd) — the new state and a follow-up Cmd
//	           to run (or nil for none). tea.Batch runs several Cmds
//	           concurrently; tea.Tick schedules a Msg for the future.
//
// So a typical cycle looks like:
//  1. tickMsg arrives → Update returns (m, Batch(fetchWallet, fetchTxs,…))
//  2. fetchWallet runs in a goroutine, calls GetWalletInfo, returns
//     walletMsg{w, err}
//  3. Update receives walletMsg, stores m.wallet = w, returns (m, nil)
//  4. View renders the new Model
package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ---- Message types ----------------------------------------------------
//
// Each one is a plain struct carrying the result of some async work. Making
// them distinct types (instead of a single union) lets Update's type switch
// pick the right branch at compile time.

// tickMsg fires every cfg.Refresh interval and drives the polling loop.
// The underlying time.Time is useful for ordering / debug logging.
type tickMsg time.Time

// One struct per RPC so we can tell in Update which fetch finished.
type walletMsg struct {
	w   WalletInfo
	err error
}
type chainMsg struct {
	c   BlockchainInfo
	err error
}
type stakingMsg struct {
	s   StakingInfo
	err error
}
type peersMsg struct {
	peers []PeerInfo
	err   error
}
type txsMsg struct {
	resp SinceBlockResponse
	err  error
}
type addrsMsg struct {
	a   []ReceivedAddress
	err error
}

// addrMineMsg carries authoritative ownership flags resolved by
// validateaddress, keyed by address, to be merged into Model.addrMine.
type addrMineMsg struct {
	mine map[string]bool
}

// txContractsMsg carries Gridcoin contract types resolved by gettransaction,
// keyed by txid, to be merged into Model.txContracts. An entry with an empty
// value means "resolved, carries no contract" — see fetchContracts.
type txContractsMsg struct {
	types map[string]string
}
type validateMsg struct {
	v   ValidateAddress
	err error
}
type sendResultMsg struct {
	txid string
	err  error
}
type signResultMsg struct {
	sig string
	err error
}
type setLabelResultMsg struct {
	err error
}
type addLabelValidateMsg struct {
	v   ValidateAddress
	err error
}
type addLabelResultMsg struct {
	err error
}

// pollsMsg carries the result of a listpolls fetch (the whole list). It echoes
// back the includeFinished scope the request was issued with so the handler can
// drop a stale reply: if the user toggles all/active (or refreshes) while a
// previous listpolls is still in flight, an older response could otherwise
// overwrite a newer view and desync the title/footer from the rows.
type pollsMsg struct {
	includeFinished bool
	polls           []Poll
	err             error
}

// pollResultMsg carries one lazily-fetched getpollresults tally, tagged with
// the poll ID it belongs to so the handler can slot it into the cache even if
// the cursor has since moved on.
type pollResultMsg struct {
	id     string
	result PollResult
	err    error
}

// pollSettleMsg fires a short time after the poll cursor last moved. It carries
// the poll ID that was selected when the timer was armed; the handler only
// starts that poll's tally if the cursor is still on it. This debounces the
// heavy getpollresults call so scrolling past polls doesn't queue one per row.
type pollSettleMsg struct{ id string }

// updateCheckMsg carries the result of a GitHub "latest release" query. One
// message type serves both callers: the silent background check (updates the
// header badge) and the modal's live check (also advances the modal step).
// manual distinguishes the two: only a manual (u-key) check may drive the modal
// state machine, so a background check that races with it — and especially a
// background *error* — can never flip the modal to a false "failed".
type updateCheckMsg struct {
	rel            releaseInfo
	missedReleases []releaseInfo
	err            error
	manual         bool
}

// updateInstallMsg carries the result of the download+verify+swap. newExe is
// the path to re-exec on success.
type updateInstallMsg struct {
	newExe string
	err    error
}

// updateTickMsg fires on the long background update-check interval.
type updateTickMsg time.Time

// pollSettleDelay is how long the cursor must rest on a poll before its tally is
// fetched. Short enough to feel instant when you stop, long enough to skip
// polls you scroll straight through.
const pollSettleDelay = 350 * time.Millisecond

// spinnerTickMsg fires on a timer (every spinnerInterval) while the refresh
// spinner is running. It is separate from tickMsg because the refresh interval
// is seconds and the spinner frame rate is ~4 Hz.
type spinnerTickMsg time.Time

// spinnerFrames is the Braille dot spinner used in the footer while
// RPC fetches are in flight. The set is 10 frames long so the spinner
// appears to rotate smoothly at spinnerInterval (250 ms per frame).
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

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
// returned Cmd (or nil) to their tea.Batch — tea.Batch silently drops nil, so
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

// fetchWallet returns a Cmd that will call GetWalletInfo on a goroutine and
// turn the result into a walletMsg. Note the Cmd "captures" the rpc pointer
// in a closure — when the Cmd runs later, it still has access to it.
func fetchWallet(rpc *RPCClient) tea.Cmd {
	return func() tea.Msg {
		w, err := rpc.GetWalletInfo()
		return walletMsg{w, err}
	}
}
func fetchChain(rpc *RPCClient) tea.Cmd {
	return func() tea.Msg {
		c, err := rpc.GetBlockchainInfo()
		return chainMsg{c, err}
	}
}
func fetchStaking(rpc *RPCClient) tea.Cmd {
	return func() tea.Msg {
		s, err := rpc.GetStakingInfo()
		return stakingMsg{s, err}
	}
}
func fetchPeers(rpc *RPCClient) tea.Cmd {
	return func() tea.Msg {
		p, err := rpc.GetPeerInfo()
		return peersMsg{p, err}
	}
}

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
func fetchTxs(rpc *RPCClient, lastBlock string) tea.Cmd {
	return func() tea.Msg {
		resp, err := rpc.ListSinceBlock(lastBlock, txRefreshDepth, true)
		return txsMsg{resp: resp, err: err}
	}
}

func fetchAddrs(rpc *RPCClient) tea.Cmd {
	return func() tea.Msg {
		a, err := rpc.ListAddressBook()
		return addrsMsg{a, err}
	}
}

// fetchAddrOwnership resolves the ownership of each given address via
// validateaddress, serially so the TUI never holds more than one daemon RPC
// worker at a time (the same good-neighbour policy as refreshAllCmd). Callers
// pass only addresses whose ownership isn't cached yet, so on an idle wallet
// this fires once per genuinely new address and then stays quiet.
//
// We need it because listreceivedbyaddress returns the whole address book —
// including foreign addresses you've merely labelled — and those carry no
// involvesWatchonly flag, so they're otherwise indistinguishable from your
// own. validateaddress.ismine is (IsMine != ISMINE_NO): true for spendable
// and watch-only addresses, false only for foreign ones — the same test the
// official Qt wallet uses to split its Receiving and Sending address lists.
func fetchAddrOwnership(rpc *RPCClient, addrs []string) tea.Cmd {
	return func() tea.Msg {
		mine := make(map[string]bool, len(addrs))
		for _, a := range addrs {
			v, err := rpc.ValidateAddress(a)
			if err != nil {
				continue // leave unresolved; retried on the next address refresh
			}
			mine[a] = v.IsMine
		}
		return addrMineMsg{mine}
	}
}

// fetchContracts resolves the Gridcoin contract type of each given
// transaction via gettransaction, one txid at a time — the same serial
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
// identical and merge idempotently, which is why this is left unguarded —
// the same trade-off fetchAddrOwnership already makes.
//
// A txid whose lookup fails is left out of the map entirely rather than
// cached as "no contract": leaving it unresolved means the next wallet
// activity — or the user opening its detail modal — retries it, where a
// wrong negative answer would stick for the whole session.
func fetchContracts(rpc *RPCClient, txids []string) tea.Cmd {
	return func() tea.Msg {
		types := make(map[string]string, len(txids))
		for _, id := range txids {
			d, err := rpc.GetTransaction(id)
			if err != nil {
				continue
			}
			// The empty string is a real answer here ("we asked, there is no
			// contract"), which is what stops us asking again every tick.
			//
			// Only the first contract is read. The field is a vector, but the
			// daemon treats one-per-transaction as the rule and indexes
			// vContracts[0] the same way throughout its own miner, voting and
			// wallet code — block validation even rejects a coinbase carrying
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

// refreshAllCmd fires all six fetches SEQUENTIALLY via tea.Sequence.
//
// tea.Sequence, unlike tea.Batch, runs its child Cmds one at a time and
// waits for each to land its Msg back through Update before starting the
// next. We use it deliberately here so the TUI only holds one
// gridcoinresearchd RPC worker thread (and one wallet lock) at any
// moment. A parallel tea.Batch of 4–5 concurrent RPCs can pin the
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

func validateAddr(rpc *RPCClient, addr string) tea.Cmd {
	return func() tea.Msg {
		v, err := rpc.ValidateAddress(addr)
		return validateMsg{v, err}
	}
}

// fetchPolls loads the governance poll list. includeFinished maps to
// listpolls's showfinished argument. Fired only when the polls screen is
// opened / refreshed / toggled, never on the refresh tick.
func fetchPolls(rpc *RPCClient, includeFinished bool) tea.Cmd {
	return func() tea.Msg {
		polls, err := rpc.ListPolls(includeFinished)
		return pollsMsg{includeFinished: includeFinished, polls: polls, err: err}
	}
}

// fetchPollResult runs the getpollresults tally for a single poll. Heavy, so
// it's fired lazily for just the poll under the cursor (see ensurePollResult).
func fetchPollResult(rpc *RPCClient, id string) tea.Cmd {
	return func() tea.Msg {
		r, err := rpc.GetPollResults(id)
		return pollResultMsg{id: id, result: r, err: err}
	}
}

// reloadPolls (re)loads the poll list for the current all/active scope and
// bumps the inflight spinner. Called on open, on "r", and after a tab toggle.
// It also clears the cached getpollresults tallies; without that,
// ensurePollResult's cache guard would keep serving each poll's first tally and
// newer votes would stay hidden until restart.
func (m *Model) reloadPolls() tea.Cmd {
	m.pollResults = make(map[string]PollResult)
	m.pollResultPending = make(map[string]bool)
	m.pollResultErr = make(map[string]string)
	spin := m.bumpInflight(1)
	return tea.Batch(fetchPolls(m.rpc, m.pollsShowFinished), spin)
}

// ensurePollResult lazily kicks off the getpollresults tally for the poll
// under the cursor, unless it's already cached or a fetch is already in
// flight. Returns nil (a no-op Cmd for tea.Batch) when there's nothing to do,
// so cursor-move handlers can call it unconditionally.
func (m *Model) ensurePollResult() tea.Cmd {
	p := m.selectedPoll()
	if p == nil || p.ID == "" {
		return nil
	}
	if _, cached := m.pollResults[p.ID]; cached {
		return nil
	}
	if m.pollResultPending[p.ID] {
		return nil
	}
	m.pollResultPending[p.ID] = true
	spin := m.bumpInflight(1)
	return tea.Batch(fetchPollResult(m.rpc, p.ID), spin)
}

// retryPollResult clears a failed tally's error for the selected poll and asks
// ensurePollResult to re-issue it — the detail popup's "r" key. A tally already
// cached or in flight is left alone.
func (m *Model) retryPollResult() tea.Cmd {
	if p := m.selectedPoll(); p != nil {
		delete(m.pollResultErr, p.ID)
	}
	return m.ensurePollResult()
}

// schedulePollSettle arms the debounce timer for the currently-selected poll.
// Used on cursor moves and after the list loads: only once the cursor has
// rested on a poll for pollSettleDelay does its (heavy) tally actually fire, so
// scrolling through the list no longer kicks off a getpollresults per row.
// Opening the detail popup with enter still fetches immediately.
func (m *Model) schedulePollSettle() tea.Cmd {
	p := m.selectedPoll()
	if p == nil || p.ID == "" {
		return nil
	}
	id := p.ID
	return tea.Tick(pollSettleDelay, func(time.Time) tea.Msg {
		return pollSettleMsg{id: id}
	})
}

// runSend performs the send-wizard's final step: unlock the wallet (if
// needed), broadcast the transaction, and ALWAYS re-lock before returning.
// The _ = rpc.WalletLock() pattern discards the return value on purpose —
// we don't want a re-lock failure to mask a successful send.
func runSend(rpc *RPCClient, addr string, amount float64, passphrase string, needsUnlock bool) tea.Cmd {
	return func() tea.Msg {
		if needsUnlock {
			if err := rpc.WalletPassphrase(passphrase, 30); err != nil {
				return sendResultMsg{err: fmt.Errorf("unlock: %w", err)}
			}
		}
		txid, err := rpc.SendToAddress(addr, amount)
		if needsUnlock {
			// Best-effort re-lock; don't mask the real error if send succeeded.
			_ = rpc.WalletLock()
		}
		return sendResultMsg{txid: txid, err: err}
	}
}

// runSign mirrors runSend's lifecycle for the signmessage RPC. needsUnlock
// is the conjunction of "wallet is encrypted" AND "wallet is currently
// locked" — an unencrypted wallet, or one the user has already unlocked
// for staking, never gets the walletpassphrase / walletlock pair sent at
// it. We only re-lock when WE were the ones who unlocked, so we don't
// trample on the user's existing unlock window.
func runSign(rpc *RPCClient, addr, message, passphrase string, needsUnlock bool) tea.Cmd {
	return func() tea.Msg {
		if needsUnlock {
			if err := rpc.WalletPassphrase(passphrase, 30); err != nil {
				return signResultMsg{err: fmt.Errorf("unlock: %w", err)}
			}
		}
		sig, err := rpc.SignMessage(addr, message)
		if needsUnlock {
			_ = rpc.WalletLock()
		}
		return signResultMsg{sig: sig, err: err}
	}
}

// runSetLabel fires the setaccount RPC and reports the outcome as a
// setLabelResultMsg. Unlike runSign/runSend there is no passphrase / unlock
// dance — setting a label is address-book metadata, not a signing operation.
func runSetLabel(rpc *RPCClient, addr, label string) tea.Cmd {
	return func() tea.Msg {
		return setLabelResultMsg{err: rpc.SetAccount(addr, label)}
	}
}

func validateAddLabelAddress(rpc *RPCClient, addr string) tea.Cmd {
	return func() tea.Msg {
		v, err := rpc.ValidateAddress(addr)
		return addLabelValidateMsg{v: v, err: err}
	}
}

func runAddLabel(rpc *RPCClient, addr, label string) tea.Cmd {
	return func() tea.Msg {
		return addLabelResultMsg{err: rpc.SetAccount(addr, label)}
	}
}

// checkUpdateCmd queries GitHub for the latest release. It runs on a goroutine
// like every other Cmd; the short HTTP timeout in fetchLatestRelease keeps a
// blocked network from wedging the loop. Note it does NOT touch m.inflight —
// the update flow is intentionally separate from the RPC refresh spinner so a
// silent background check never makes the dashboard footer flash "refreshing".
func checkUpdateCmd(manual bool) tea.Cmd {
	return func() tea.Msg {
		rel, err := fetchLatestRelease(updateAPIBase)
		if err != nil {
			return updateCheckMsg{err: err, manual: manual}
		}
		var releases []releaseInfo
		if manual {
			releases, err = fetchReleases(updateAPIBase)
			if err != nil {
				return updateCheckMsg{err: err, manual: manual}
			}
		}
		return updateCheckMsg{rel: rel, missedReleases: missedReleases(version, rel, releases), manual: manual}
	}
}

// runUpdate downloads, verifies and swaps the binary for the given release.
func runUpdate(rel releaseInfo) tea.Cmd {
	return func() tea.Msg {
		newExe, err := applyUpdate(rel)
		return updateInstallMsg{newExe: newExe, err: err}
	}
}

// updateTickCmd schedules the next background update check.
func (m *Model) updateTickCmd() tea.Cmd {
	return tea.Tick(updateCheckInterval, func(t time.Time) tea.Msg { return updateTickMsg(t) })
}

// ---- Init / Update ----------------------------------------------------

// Init is called once when the program starts. Whatever Cmd it returns is
// the first action the runtime executes — we kick off the recurring tick,
// the initial RPC fetches, and the spinner loop (which will self-stop
// once all six fetches land because NewModel pre-seeded inflight=6).
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.tickCmd(), m.refreshAllCmd(), spinnerTickCmd()}
	if !m.cfg.NoUpdateCheck {
		// Fire one (silent, non-manual) check shortly after launch, and arm the
		// periodic re-check.
		cmds = append(cmds, checkUpdateCmd(false), m.updateTickCmd())
	}
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
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
		return m, spinnerTickCmd()

	case walletMsg:
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
	case chainMsg:
		m.finishFetch()
		if msg.err != nil {
			m.walletErr = msg.err.Error()
		} else {
			m.chain = msg.c
		}
		return m, nil
	case stakingMsg:
		m.finishFetch()
		if msg.err != nil {
			m.walletErr = msg.err.Error()
		} else {
			m.staking = msg.s
		}
		return m, nil
	case peersMsg:
		m.finishFetch()
		if msg.err != nil {
			m.walletErr = msg.err.Error()
		} else {
			m.peersTotal = len(msg.peers)
			m.peersIn = 0
			for _, p := range msg.peers {
				if p.Inbound {
					m.peersIn++
				}
			}
			m.peersOut = m.peersTotal - m.peersIn
			m.peersLoaded = true
		}
		return m, nil
	case txsMsg:
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
			// would hold two daemon RPC workers — the thing refreshAllCmd
			// goes out of its way to avoid. The spinner tick stays outside
			// the sequence: it isn't an RPC, and burying it behind the
			// fetches would delay the first frame until they finished.
			spin := m.bumpInflight(len(cmds))
			return m, tea.Batch(tea.Sequence(cmds...), spin)
		}
		return m, nil
	case addrsMsg:
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

	case addrMineMsg:
		m.finishFetch()
		for a, mine := range msg.mine {
			m.addrMine[a] = mine
		}
		// Resolving ownership can move a row out of the active tab (e.g. an
		// unknown row turns out foreign and leaves the Mine view), so re-clamp
		// the cursor against the now-current visible length.
		m.addrCursor = clampCursor(m.addrCursor, len(m.visibleAddresses()))
		return m, nil

	case txContractsMsg:
		m.finishFetch()
		for id, t := range msg.types {
			m.txContracts[id] = t
		}
		return m, nil

	case validateMsg:
		m.send.validating = false
		if msg.err != nil {
			m.send.errMsg = msg.err.Error()
			return m, nil
		}
		if !msg.v.IsValid {
			m.send.errMsg = "address is not valid"
			return m, nil
		}
		m.send.errMsg = ""
		m.send.step = sendStepAmount
		m.send.address.Blur()
		m.send.amount.Focus()
		return m, nil

	case sendResultMsg:
		m.send.busy = false
		m.send.step = sendStepResult
		if msg.err != nil {
			m.send.resultErr = msg.err.Error()
		} else {
			m.send.resultTxID = msg.txid
		}
		// Refresh the tx list so the just-broadcast transaction appears.
		spin := m.bumpInflight(1)
		return m, tea.Batch(fetchTxs(m.rpc, m.txsLastBlock), spin)

	case signResultMsg:
		m.sign.busy = false
		m.sign.step = signStepResult
		if msg.err != nil {
			m.sign.resultErr = msg.err.Error()
		} else {
			m.sign.resultSig = msg.sig
		}
		return m, nil

	case setLabelResultMsg:
		m.edit.busy = false
		if msg.err != nil {
			// Keep the modal open so the user can read the error and retry.
			m.edit.errMsg = msg.err.Error()
			return m, nil
		}
		// Success: close the modal and refresh the address list so the new
		// label (Gridcoin's legacy "account") shows via DisplayLabel.
		m.edit.blurAll()
		m.mode = modeDashboard
		spin := m.bumpInflight(1)
		return m, tea.Batch(fetchAddrs(m.rpc), spin)

	case addLabelValidateMsg:
		m.add.validating = false
		if msg.err != nil {
			m.add.errMsg = msg.err.Error()
			return m, nil
		}
		if !msg.v.IsValid {
			m.add.errMsg = "address is not valid"
			return m, nil
		}
		m.add.busy = true
		m.add.errMsg = ""
		return m, runAddLabel(m.rpc, m.add.address.Value(), m.add.label.Value())

	case addLabelResultMsg:
		m.add.busy = false
		if msg.err != nil {
			m.add.errMsg = msg.err.Error()
			return m, nil
		}
		m.add.blurAll()
		m.mode = modeDashboard
		spin := m.bumpInflight(1)
		return m, tea.Batch(fetchAddrs(m.rpc), spin)

	case pollsMsg:
		m.finishFetch()
		// Drop a reply whose scope no longer matches what the screen is now
		// showing (the user toggled all/active while this was in flight). A
		// newer request for the current scope is the authoritative one.
		if msg.includeFinished != m.pollsShowFinished {
			return m, nil
		}
		if msg.err != nil {
			m.pollsErr = msg.err.Error()
			m.pollsLoaded = true
			return m, nil
		}
		m.polls = msg.polls
		// Order newest-first by posting date so the most recent polls are at
		// the top. Unparseable timestamps sort as the zero time and sink to the
		// bottom. SliceStable keeps the daemon's order among equal timestamps.
		sort.SliceStable(m.polls, func(i, j int) bool {
			return ParsePollTime(m.polls[i].Timestamp).After(ParsePollTime(m.polls[j].Timestamp))
		})
		m.pollsLoaded = true
		m.pollsErr = ""
		m.pollCursor = clampCursor(m.pollCursor, len(m.polls))
		// Debounce the tally for whichever poll the cursor now rests on, so a
		// quick reload+scroll doesn't fetch one you're about to leave.
		return m, m.schedulePollSettle()

	case pollSettleMsg:
		// The debounce timer fired: only fetch if the cursor is still on the
		// poll that armed it. Otherwise the user moved on and a later timer
		// (armed by that move) will handle the new selection.
		if p := m.selectedPoll(); p != nil && p.ID == msg.id {
			return m, m.ensurePollResult()
		}
		return m, nil

	case pollResultMsg:
		m.finishFetch()
		delete(m.pollResultPending, msg.id)
		if msg.err != nil {
			// Record the error so the detail popup can show why the tally
			// failed (rather than a perpetual "tallying…") and offer a retry.
			// The list row still falls back to the "N votes" count.
			m.pollResultErr[msg.id] = msg.err.Error()
			return m, nil
		}
		delete(m.pollResultErr, msg.id)
		m.pollResults[msg.id] = msg.result
		return m, nil

	case updateTickMsg:
		// Long-interval background re-check. Re-arm the timer and fire another
		// silent check (respecting a late opt-out, though Init won't arm this
		// when NoUpdateCheck is set).
		if m.cfg.NoUpdateCheck {
			return m, nil
		}
		return m, tea.Batch(m.updateTickCmd(), checkUpdateCmd(false))

	case updateCheckMsg:
		if msg.err != nil {
			// Only a manual check may surface a failure in the modal. A racing
			// background error must never flip a manual check the user is
			// waiting on to "failed" — the manual success would then be unable
			// to recover the modal (its advance only fires from "checking").
			if msg.manual && m.mode == modeUpdate && m.update.step == updateStepChecking {
				m.update.step = updateStepFailed
				m.update.errMsg = msg.err.Error()
			}
			return m, nil
		}
		// Any successful check — manual or background — refreshes the badge.
		// Only a manual check owns the modal payload; background checks do not
		// fetch missed release notes and must not collapse an open changelog to
		// latest-only while the user is reading it.
		m.latestVersion = strings.TrimPrefix(msg.rel.TagName, "v")
		m.updateAvailable = isNewer(msg.rel.TagName, version)
		if msg.manual {
			m.update.rel = msg.rel
			m.update.missedReleases = msg.missedReleases
			if len(m.update.missedReleases) == 0 {
				m.update.missedReleases = missedReleases(version, msg.rel, nil)
			}
		}
		// Only a manual check drives the modal, and only from its "checking"
		// state. That ignores a background check landing while the user reads
		// the changelog or is mid-install, so it can't yank the view around.
		if msg.manual && m.mode == modeUpdate && m.update.step == updateStepChecking {
			if m.updateAvailable || version == "dev" {
				m.update.step = updateStepAvailable
			} else {
				m.update.step = updateStepUpToDate
			}
		}
		return m, nil

	case updateInstallMsg:
		if msg.err != nil {
			m.update.step = updateStepFailed
			m.update.errMsg = msg.err.Error()
			return m, nil
		}
		// Binary swapped. Record the path and quit; main.go re-execs it after
		// Bubble Tea restores the terminal.
		m.restartExe = msg.newExe
		return m, tea.Quit

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// ---- Key handling -----------------------------------------------------
//
// Key handling branches by mode first so modal screens get their own
// isolated keybinding scope. The dashboard handler is the "outer" one.

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeSend:
		return m.handleSendKey(msg)
	case modeSign:
		return m.handleSignKey(msg)
	case modeConfig:
		return m.handleConfigKey(msg)
	case modeTxDetail:
		// The detail modal is read-only: any of these keys closes it.
		if k := msg.String(); k == "esc" || k == "q" || k == "enter" {
			m.mode = modeDashboard
		}
		return m, nil
	case modeEditLabel:
		return m.handleEditLabelKey(msg)
	case modeAddLabel:
		return m.handleAddLabelKey(msg)
	case modeHelp:
		// The help sheet is read-only; any key dismisses it.
		m.mode = modeDashboard
		return m, nil
	case modePolls:
		return m.handlePollsKey(msg)
	case modePollDetail:
		switch msg.String() {
		case "esc", "q", "enter":
			m.mode = modePolls
			return m, nil
		case "r":
			// Retry a failed tally without leaving the popup.
			return m, m.retryPollResult()
		}
		return m, nil
	case modeUpdate:
		return m.handleUpdateKey(msg)
	}
	// Dashboard-mode keys.
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		// Anti-pileup: if a previous refresh is still running, ignore
		// the keystroke instead of stacking another 6-fetch sequence
		// behind it. The spinner already tells the user a refresh is
		// in progress.
		if m.inflight > 0 {
			return m, nil
		}
		spin := m.bumpInflight(6)
		return m, tea.Batch(m.refreshAllCmd(), spin)
	case "s":
		m.openSendModal()
		return m, nil
	case "m":
		m.openSignModal()
		return m, nil
	case "e":
		// Edit the label of the highlighted address — only meaningful when the
		// addresses panel is focused and has a valid selection (selectedAddress
		// returns nil for an empty tab or out-of-range cursor).
		if m.focusedArea == focusAddr && m.selectedAddress() != nil {
			m.openEditLabelModal()
		}
		return m, nil
	case "n":
		m.openAddLabelModal()
		return m, nil
	case "c":
		m.openConfigModal()
		return m, nil
	case "u":
		// Open the Updates modal and always kick off a fresh check — this is
		// also the manual "check now", so pressing u never shows a stale result.
		m.mode = modeUpdate
		m.update.step = updateStepChecking
		m.update.errMsg = ""
		m.update.missedReleases = nil
		return m, checkUpdateCmd(true)
	case "p":
		// Open the full-screen polls list and (re)load it. Lazy on purpose:
		// polls are never fetched on the refresh tick.
		m.mode = modePolls
		return m, m.reloadPolls()
	case "?":
		m.mode = modeHelp
		return m, nil
	case "a":
		m.anonymous = !m.anonymous
		return m, nil
	case "tab":
		// Toggle the arrow-key focus between the tx list and the addresses panel.
		if m.focusedArea == focusTx {
			m.focusedArea = focusAddr
		} else {
			m.focusedArea = focusTx
		}
		// Start each visit to the address panel from the left edge.
		m.addrHScroll = 0
		return m, nil
	case "left", "h":
		if m.focusedArea == focusAddr && m.addrHScroll > 0 {
			m.addrHScroll--
		}
		return m, nil
	case "right", "l":
		if m.focusedArea == focusAddr && m.addrHScroll < m.addrMaxScroll(m.visibleAddresses(), m.panelRowWidth()) {
			m.addrHScroll++
		}
		return m, nil
	case "1", "2", "3":
		// Switch the addresses tab (Mine / Others / All). Reset the cursor and
		// horizontal pan so each tab starts at the top-left, since the visible
		// rows differ. The key digit maps directly onto the addrTab enum.
		m.addrTab = addrTab(msg.String()[0] - '1')
		m.addrCursor = 0
		m.addrHScroll = 0
		return m, nil
	case "enter":
		// Enter only opens the tx detail modal — pressing it while the
		// addresses panel is focused is a no-op on purpose.
		if m.focusedArea == focusTx && len(m.txs) > 0 && m.txCursor >= 0 && m.txCursor < len(m.txs) {
			m.mode = modeTxDetail
			// Last-resort contract lookup for the row the user is actually
			// looking at. Normally the type is already cached from the
			// txsMsg batch, but that batch drops txids whose lookup failed,
			// so this doubles as the retry path: opening the modal asks
			// again, and it fills itself in when the reply lands. If this
			// attempt fails too the field keeps saying "resolving…" until
			// the next wallet change or the next time the modal is opened.
			tx := m.txs[m.txCursor]
			if _, ok := m.txContracts[tx.TxID]; !ok && IsContractCandidate(tx) {
				spin := m.bumpInflight(1)
				return m, tea.Batch(fetchContracts(m.rpc, []string{tx.TxID}), spin)
			}
		}
		return m, nil
	case "up", "k":
		m.scrollBy(-1)
		return m, nil
	case "down", "j":
		m.scrollBy(1)
		return m, nil
	case "pgup", "ctrl+u":
		m.scrollBy(-pageSize)
		return m, nil
	case "pgdown", "ctrl+d":
		m.scrollBy(pageSize)
		return m, nil
	case "+", "=":
		// Grow the addresses panel (push the divider down). Seed from the
		// current effective height so the first press never jumps, and clamp
		// so Transactions keeps its 3-row minimum.
		available := m.availableBodyHeight()
		m.addrPanelRows = m.clampPanelRows(m.addrPanelHeight(available)+1, available)
		return m, nil
	case "-":
		// Shrink the addresses panel (push the divider up), floored at 3 rows.
		available := m.availableBodyHeight()
		m.addrPanelRows = m.clampPanelRows(m.addrPanelHeight(available)-1, available)
		return m, nil
	case "0":
		// Snap the split back to the auto-computed default.
		m.addrPanelRows = 0
		return m, nil
	case "g", "home":
		m.scrollTo(0)
		return m, nil
	case "G", "end":
		_, length := m.focusedList()
		m.scrollTo(length - 1)
		return m, nil
	}
	return m, nil
}

// handleUpdateKey drives the self-update modal. The current step decides which
// keys do what: while a check or install is in flight most keys are inert so a
// stray keystroke can't interrupt it.
func (m Model) handleUpdateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.update.step {
	case updateStepInstalling:
		// Don't let any keystroke interrupt an in-flight download/swap.
		return m, nil
	case updateStepAvailable:
		switch msg.String() {
		case "y", "enter":
			m.update.step = updateStepInstalling
			m.update.errMsg = ""
			return m, runUpdate(m.update.rel)
		case "n", "esc", "q":
			m.mode = modeDashboard
		}
		return m, nil
	default:
		// checking / upToDate / failed: esc/q/enter closes; other keys ignored.
		// Closing during a check is fine — the goroutine still finishes and
		// updates the badge; it just no longer has a modal to advance.
		switch msg.String() {
		case "esc", "q", "enter":
			m.mode = modeDashboard
		}
		return m, nil
	}
}

// handlePollsKey drives the full-screen polls list. Navigation is
// self-contained (it moves m.pollCursor directly rather than going through
// focusedList, which only knows the dashboard's tx/address panels). Every
// cursor move returns ensurePollResult() so the lazy tally for the newly
// selected poll starts fetching.
func (m Model) handlePollsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Cases that don't just move the cursor return early. The remaining
	// movement keys share one clamp + ensurePollResult tail, so they only set a
	// target and fall through. clampCursor pins the target to [0, len-1], so
	// g/G (0 and len-1) fold in without special-casing.
	target := m.pollCursor
	switch msg.String() {
	case "esc", "q":
		m.mode = modeDashboard
		return m, nil
	case "r":
		return m, m.reloadPolls()
	case "tab":
		// Toggle between all polls (incl. finished) and active only, then
		// reload. Reset the cursor since the row set changes.
		m.pollsShowFinished = !m.pollsShowFinished
		m.pollCursor = 0
		return m, m.reloadPolls()
	case "enter":
		// Open the detail popup for the selected poll. Its tally was usually
		// fetched already when the cursor landed here, but ensurePollResult also
		// (re)starts it if the list prefetch hasn't run or a prior attempt
		// errored — a no-op when the tally is cached or already in flight.
		if m.selectedPoll() == nil {
			return m, nil
		}
		m.mode = modePollDetail
		return m, m.ensurePollResult()
	case "up", "k":
		target = m.pollCursor - 1
	case "down", "j":
		target = m.pollCursor + 1
	case "pgup", "ctrl+u":
		target = m.pollCursor - pageSize
	case "pgdown", "ctrl+d":
		target = m.pollCursor + pageSize
	case "g", "home":
		target = 0
	case "G", "end":
		target = len(m.polls) - 1
	default:
		return m, nil
	}
	m.pollCursor = clampCursor(target, len(m.polls))
	return m, m.schedulePollSettle()
}

// pageSize is the fixed step used by pgup/pgdn/ctrl+u/ctrl+d. Keeping it
// constant (rather than computing it from the panel's visible height) means
// the scroll speed is predictable regardless of which panel is focused or
// how tall the terminal currently is.
const pageSize = 10

// focusedList returns a pointer to the cursor field of the currently-
// focused scrollable list and the length of its backing slice. Every
// scroll helper goes through this accessor so the focusedArea dispatch
// lives in exactly one place — adding a third panel later only needs a
// new case here, not in every helper that scrolls.
func (m *Model) focusedList() (*int, int) {
	if m.focusedArea == focusAddr {
		// Scroll within the active tab, not the full book.
		return &m.addrCursor, len(m.visibleAddresses())
	}
	return &m.txCursor, len(m.txs)
}

// scrollBy moves the cursor of the currently-focused list by delta rows,
// clamped to [0, len-1]. Positive delta scrolls down.
func (m *Model) scrollBy(delta int) {
	cursor, length := m.focusedList()
	*cursor = clampCursor(*cursor+delta, length)
	if m.focusedArea == focusTx {
		m.txOffset = m.txWindowOffset(m.txListRows())
	}
}

// scrollTo jumps the cursor of the currently-focused list to an absolute
// position. Negative values clamp to 0 and values past the end clamp to
// the last row — the caller is free to pass length-1 for "go to end".
func (m *Model) scrollTo(pos int) {
	cursor, length := m.focusedList()
	*cursor = clampCursor(pos, length)
	if m.focusedArea == focusTx {
		m.txOffset = m.txWindowOffset(m.txListRows())
	}
}

// clampCursor pins a desired cursor position to the valid range
// [0, length-1]. An empty list always clamps to 0.
func clampCursor(c, length int) int {
	if length == 0 {
		return 0
	}
	if c < 0 {
		return 0
	}
	if c >= length {
		return length - 1
	}
	return c
}

// openSendModal resets the send wizard and focuses the address field. It
// preserves the existing textinput.Model instances so their placeholder /
// width / mask settings survive.
func (m *Model) openSendModal() {
	m.mode = modeSend
	m.send = sendState{
		step:            sendStepAddress,
		recipientCursor: 0,
		address:         m.send.address,
		amount:          m.send.amount,
		passphrase:      m.send.passphrase,
		needsUnlock:     m.wallet.IsLocked(),
	}
	m.send.address.SetValue("")
	m.send.amount.SetValue("")
	m.send.passphrase.SetValue("")
	m.send.address.Focus()
}

// handleSendKey is the send-wizard's input handler. It is a small
// state machine: the current m.send.step decides which keys do what.
func (m Model) handleSendKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.send.recipientOpen && key == "esc" {
		m.send.recipientOpen = false
		m.send.recipientHScroll = 0
		m.send.address.Focus()
		return m, nil
	}
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.send.blurAll()
		return m, nil
	}
	if m.send.busy {
		return m, nil // ignore input while the final send RPC is in flight
	}
	if m.send.validating {
		return m, nil // do not race multiple validateaddress requests
	}
	switch m.send.step {
	case sendStepAddress:
		recipients := m.sendRecipients()
		if m.send.recipientOpen {
			switch key {
			case "left", "h":
				if m.send.recipientHScroll > 0 {
					m.send.recipientHScroll--
				}
			case "right", "l":
				if m.send.recipientHScroll < m.addrMaxScroll(recipients, 50) {
					m.send.recipientHScroll++
				}
			case "up", "k":
				if m.send.recipientCursor > 0 {
					m.send.recipientCursor--
				}
			case "down", "j":
				if m.send.recipientCursor < len(recipients)-1 {
					m.send.recipientCursor++
				}
			case "home", "g":
				m.send.recipientCursor = 0
			case "end", "G":
				if len(recipients) > 0 {
					m.send.recipientCursor = len(recipients) - 1
				}
			case "enter", "tab", "shift+tab":
				if m.send.recipientCursor < len(recipients) {
					m.send.address.SetValue(recipients[m.send.recipientCursor].Address)
				}
				m.send.recipientOpen = false
				m.send.address.Focus()
			}
			return m, nil
		}
		if (key == "tab" || key == "down") && len(recipients) > 0 {
			m.send.recipientOpen = true
			m.send.recipientHScroll = 0
			m.send.address.Blur()
			return m, nil
		}
		if key == "enter" {
			// Fire a validate RPC; the validateMsg handler advances the step.
			if v := m.send.address.Value(); v != "" {
				m.send.validating = true
				m.send.errMsg = ""
				return m, validateAddr(m.rpc, v)
			}
			return m, nil
		}
		// Any other key: hand it to the textinput so it can edit itself.
		// The textinput returns a new Model we have to assign back.
		var cmd tea.Cmd
		m.send.address, cmd = m.send.address.Update(msg)
		return m, cmd
	case sendStepAmount:
		if key == "enter" {
			amt, err := strconv.ParseFloat(m.send.amount.Value(), 64)
			if err != nil || amt <= 0 {
				m.send.errMsg = "enter a positive amount"
				return m, nil
			}
			if amt > m.wallet.Balance {
				avail := FormatGRCPlain(m.wallet.Balance)
				if m.anonymous {
					avail = MaskedAmount
				}
				m.send.errMsg = fmt.Sprintf("amount exceeds balance (%s available)", avail)
				return m, nil
			}
			m.send.errMsg = ""
			m.send.amountValue = amt
			m.send.amount.Blur()
			if m.send.needsUnlock {
				m.send.step = sendStepPassphrase
				m.send.passphrase.Focus()
			} else {
				m.send.step = sendStepConfirm
			}
			return m, nil
		}
		// backspace on an empty amount field walks us back to the address step.
		if key == "backspace" && m.send.amount.Value() == "" {
			m.send.step = sendStepAddress
			m.send.amount.Blur()
			m.send.address.Focus()
			return m, nil
		}
		var cmd tea.Cmd
		m.send.amount, cmd = m.send.amount.Update(msg)
		return m, cmd
	case sendStepPassphrase:
		if key == "enter" {
			if m.send.passphrase.Value() == "" {
				m.send.errMsg = "passphrase required"
				return m, nil
			}
			m.send.errMsg = ""
			m.send.passphrase.Blur()
			m.send.step = sendStepConfirm
			return m, nil
		}
		var cmd tea.Cmd
		m.send.passphrase, cmd = m.send.passphrase.Update(msg)
		return m, cmd
	case sendStepConfirm:
		if key == "y" || key == "enter" {
			m.send.busy = true
			return m, runSend(m.rpc, m.send.address.Value(), m.send.amountValue,
				m.send.passphrase.Value(), m.send.needsUnlock)
		}
		if key == "n" {
			m.mode = modeDashboard
			return m, nil
		}
		return m, nil
	case sendStepResult:
		// Any key dismisses the result screen.
		m.mode = modeDashboard
		return m, nil
	}
	return m, nil
}

// ---- Sign-message modal -----------------------------------------------

// openSignModal resets the sign wizard, pre-filling the address from the
// currently selected entry in the My Addresses panel when that panel has
// focus. Pre-filling makes the common case (sign with one of my own
// addresses) zero-friction; falling back to an empty field keeps the
// modal usable when triggered from the tx panel or before addresses
// have loaded.
//
// needsUnlock follows the same UnlockedUntil tri-state contract used by
// the send wizard: nil = unencrypted, *v == 0 = encrypted+locked,
// *v > 0 = encrypted+already unlocked. We only ever prompt for a
// passphrase in the second case.
func (m *Model) openSignModal() {
	m.mode = modeSign
	m.sign = signState{
		step:        signStepAddress,
		address:     m.sign.address,
		message:     m.sign.message,
		passphrase:  m.sign.passphrase,
		needsUnlock: m.wallet.IsLocked(),
	}
	m.sign.address.SetValue("")
	m.sign.message.SetValue("")
	m.sign.passphrase.SetValue("")

	// Pre-fill from the highlighted address when the addresses panel is
	// focused and has a selection. Skip straight to the message step in that
	// case so the user doesn't have to press enter on a field that is
	// already correct.
	if sel := m.selectedAddress(); m.focusedArea == focusAddr && sel != nil {
		m.sign.address.SetValue(sel.Address)
		m.sign.step = signStepMessage
		m.sign.message.Focus()
		return
	}
	m.sign.address.Focus()
}

// handleSignKey is the sign-wizard's input handler. State machine: the
// current m.sign.step decides which keys do what. Mirrors handleSendKey,
// minus the amount/balance check and the confirm step (signing has no
// fund risk and nothing to broadcast).
func (m Model) handleSignKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.sign.blurAll()
		return m, nil
	}
	if m.sign.busy {
		return m, nil // ignore input while signmessage RPC is in flight
	}
	switch m.sign.step {
	case signStepAddress:
		if key == "enter" {
			if v := strings.TrimSpace(m.sign.address.Value()); v != "" {
				m.sign.errMsg = ""
				m.sign.address.Blur()
				m.sign.step = signStepMessage
				m.sign.message.Focus()
				return m, nil
			}
			m.sign.errMsg = "address required"
			return m, nil
		}
		var cmd tea.Cmd
		m.sign.address, cmd = m.sign.address.Update(msg)
		return m, cmd
	case signStepMessage:
		if key == "enter" {
			if m.sign.message.Value() == "" {
				m.sign.errMsg = "message cannot be empty"
				return m, nil
			}
			m.sign.errMsg = ""
			m.sign.message.Blur()
			if m.sign.needsUnlock {
				m.sign.step = signStepPassphrase
				m.sign.passphrase.Focus()
				return m, nil
			}
			// Wallet is unencrypted or already unlocked — no passphrase needed.
			m.sign.busy = true
			return m, runSign(m.rpc, m.sign.address.Value(),
				m.sign.message.Value(), "", false)
		}
		if key == "backspace" && m.sign.message.Value() == "" {
			m.sign.step = signStepAddress
			m.sign.message.Blur()
			m.sign.address.Focus()
			return m, nil
		}
		var cmd tea.Cmd
		m.sign.message, cmd = m.sign.message.Update(msg)
		return m, cmd
	case signStepPassphrase:
		if key == "enter" {
			if m.sign.passphrase.Value() == "" {
				m.sign.errMsg = "passphrase required"
				return m, nil
			}
			m.sign.errMsg = ""
			m.sign.busy = true
			return m, runSign(m.rpc, m.sign.address.Value(),
				m.sign.message.Value(), m.sign.passphrase.Value(), true)
		}
		var cmd tea.Cmd
		m.sign.passphrase, cmd = m.sign.passphrase.Update(msg)
		return m, cmd
	case signStepResult:
		// Any key dismisses the result screen.
		m.mode = modeDashboard
		m.sign.blurAll()
		return m, nil
	}
	return m, nil
}

// ---- Edit-label modal -------------------------------------------------

// openEditLabelModal opens the edit-label modal pre-filled with the
// highlighted address's current label, so the user edits in place. It no-ops
// when there's no valid selection; the caller (handleKey "e") already gates on
// that, but reading through selectedAddress keeps the modal self-guarding.
func (m *Model) openEditLabelModal() {
	sel := m.selectedAddress()
	if sel == nil {
		return
	}
	m.mode = modeEditLabel
	// Reset the struct to clear any stale busy/errMsg from a previous open,
	// keeping the configured textinput (placeholder/width).
	m.edit = editLabelState{
		label:   m.edit.label,
		address: sel.Address,
	}
	m.edit.label.SetValue(sel.DisplayLabel())
	m.edit.label.CursorEnd()
	m.edit.label.Focus()
}

// handleEditLabelKey drives the single-input edit-label modal: esc cancels,
// enter submits the setaccount RPC (an empty value clears the label), input is
// ignored while the RPC is in flight, and every other key edits the textinput.
// There is no result phase — success closes the modal in the setLabelResultMsg
// handler, and an error returns here with the modal still open.
func (m Model) handleEditLabelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.edit.blurAll()
		return m, nil
	}
	if m.edit.busy {
		return m, nil // ignore input while setaccount is running
	}
	if key == "enter" {
		// Empty value is allowed; it clears the label.
		m.edit.errMsg = ""
		m.edit.busy = true
		return m, runSetLabel(m.rpc, m.edit.address, m.edit.label.Value())
	}
	var cmd tea.Cmd
	m.edit.label, cmd = m.edit.label.Update(msg)
	return m, cmd
}

// openAddLabelModal prepares a blank address-book form. The daemon accepts
// labels for external recipient addresses as well as wallet-owned ones, so
// this is intentionally available regardless of the focused dashboard panel.
func (m *Model) openAddLabelModal() {
	m.mode = modeAddLabel
	m.add = addLabelState{address: m.add.address, label: m.add.label}
	m.add.address.SetValue("")
	m.add.label.SetValue("")
	m.add.address.Focus()
}

func (m *Model) focusAddLabelField(field addLabelField) {
	m.add.blurAll()
	m.add.focused = field
	if field == addLabelAddress {
		m.add.address.Focus()
	} else {
		m.add.label.Focus()
	}
}

// handleAddLabelKey edits the two-field address-book form. Saving always
// validates the current address first, so a user can return to and change it
// after filling the label without bypassing validation.
func (m Model) handleAddLabelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.add.blurAll()
		return m, nil
	}
	if m.add.validating || m.add.busy {
		return m, nil
	}
	switch key {
	case "tab", "down":
		m.focusAddLabelField((m.add.focused + 1) % 2)
		return m, nil
	case "shift+tab", "up":
		m.focusAddLabelField((m.add.focused - 1 + 2) % 2)
		return m, nil
	case "enter":
		if m.add.focused == addLabelAddress {
			m.focusAddLabelField(addLabelName)
			return m, nil
		}
		if m.add.address.Value() == "" {
			m.add.errMsg = "address is required"
			m.focusAddLabelField(addLabelAddress)
			return m, nil
		}
		if m.add.label.Value() == "" {
			m.add.errMsg = "label is required"
			return m, nil
		}
		m.add.errMsg = ""
		m.add.validating = true
		return m, validateAddLabelAddress(m.rpc, m.add.address.Value())
	}
	var cmd tea.Cmd
	if m.add.focused == addLabelAddress {
		m.add.address, cmd = m.add.address.Update(msg)
	} else {
		m.add.label, cmd = m.add.label.Update(msg)
	}
	return m, cmd
}

// ---- Config modal -----------------------------------------------------

func (m *Model) openConfigModal() {
	m.mode = modeConfig
	m.conf = newConfigState(m.cfg)
	m.conf.focused = cfgFieldNetwork
}

func (m *Model) focusConfigField(f configField) {
	m.conf.blurAll()
	m.conf.focused = f
	if ti := m.conf.inputFor(f); ti != nil {
		ti.Focus()
	}
}

func (m Model) handleConfigKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.conf.blurAll()
		return m, nil
	}
	// Navigation keys work regardless of which row is focused.
	switch key {
	case "tab", "down":
		// Wrap-around: modulo by cfgFieldCount cycles the focus through rows.
		m.focusConfigField((m.conf.focused + 1) % cfgFieldCount)
		return m, nil
	case "shift+tab", "up":
		m.focusConfigField((m.conf.focused - 1 + cfgFieldCount) % cfgFieldCount)
		return m, nil
	}

	// Per-field key handling.
	switch m.conf.focused {
	case cfgFieldNetwork:
		if key == " " || key == "enter" || key == "left" || key == "right" || key == "t" || key == "m" {
			// If the port field still holds the old network's default, migrate
			// it to the new network's default so users don't have to remember.
			oldDefault := defaultPort(m.conf.testnet)
			m.conf.testnet = !m.conf.testnet
			if strings.TrimSpace(m.conf.port.Value()) == oldDefault {
				m.conf.port.SetValue(defaultPort(m.conf.testnet))
			}
		}
		return m, nil
	case cfgFieldApply:
		if key == "enter" || key == " " {
			return m.applyConfig()
		}
		return m, nil
	}

	// Any other focused field is a textinput — delegate the keystroke.
	if ti := m.conf.inputFor(m.conf.focused); ti != nil {
		var cmd tea.Cmd
		*ti, cmd = ti.Update(msg)
		return m, cmd
	}
	return m, nil
}

// applyConfig validates the form, copies the values into m.cfg, rebuilds
// the RPC client against the new endpoint, clears the per-source caches,
// and kicks off a fresh refresh batch. Anything typed into the form that
// fails validation leaves the modal open with an errMsg.
func (m Model) applyConfig() (tea.Model, tea.Cmd) {
	host := strings.TrimSpace(m.conf.host.Value())
	if host == "" {
		m.conf.errMsg = "host cannot be empty"
		return m, nil
	}
	port := strings.TrimSpace(m.conf.port.Value())
	if _, err := strconv.Atoi(port); err != nil || port == "" {
		m.conf.errMsg = "port must be a number"
		return m, nil
	}
	refresh, err := time.ParseDuration(strings.TrimSpace(m.conf.refresh.Value()))
	if err != nil || refresh < time.Second {
		m.conf.errMsg = "refresh must be a duration >= 1s (e.g. 5s, 30s, 1m)"
		return m, nil
	}
	m.conf.errMsg = ""

	m.cfg.Testnet = m.conf.testnet
	if m.conf.testnet {
		m.cfg.NetworkName = "testnet"
	} else {
		m.cfg.NetworkName = "mainnet"
	}
	// Repaint the chrome so a network toggle takes effect immediately rather
	// than waiting for a restart.
	applyNetworkPalette(m.cfg.Testnet)
	m.cfg.Host = host
	m.cfg.Port = port
	m.cfg.User = strings.TrimSpace(m.conf.user.Value())
	// m.cfg.Password is intentionally NOT touched here — the password is
	// read-only in the config modal, so we preserve whatever was resolved
	// at startup from flag/env/conf.
	m.cfg.Refresh = refresh

	// Rebuild the RPC client against the new endpoint and flush every
	// cached response / error so the dashboard starts fresh.
	m.rpc = NewRPCClient(m.cfg)
	m.loaded = false
	m.txsLoaded = false
	m.addrsLoaded = false
	m.peersLoaded = false
	m.txs = nil
	m.txsLastBlock = "" // force a full re-seed against the new daemon
	m.addresses = nil
	m.walletErr = ""
	m.txsErr = ""
	m.addrsErr = ""
	m.mode = modeDashboard
	// Don't start a new tickCmd here: the lineage seeded in Init re-arms itself
	// on every tickMsg and never stops, so it already keeps polling at the new
	// cfg.Refresh. Starting another would leak a second self-re-arming tick (and
	// double the refresh rate) on every Apply.
	spin := m.bumpInflight(6)
	return m, tea.Batch(m.refreshAllCmd(), spin)
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

func makeTxKey(tx Transaction) txKey {
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
// newest-first by Time — but only when an append actually happened.
// An idle wallet's listsinceblock response just re-asserts entries we
// already have, so hasNew stays false, the in-place updates preserve
// the existing ordering, and we skip the O(n log n) sort.
//
// The second return value reports whether any entry in the delta was
// genuinely new. Callers use that signal to trigger an addresses
// refresh only when there's actual wallet activity instead of polling
// the expensive listreceivedbyaddress RPC on every tick.
func mergeTransactions(existing, delta []Transaction) ([]Transaction, bool) {
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
