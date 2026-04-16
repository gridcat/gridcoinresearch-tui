// This file is the reactive heart of the TUI. Bubble Tea's central loop
// calls our Update method once per incoming message; Update decides how
// the Model should change and what side-effect to run next.
//
// Key Bubble Tea concepts used here:
//
//   tea.Msg  — any value that describes "something happened". Can be a
//              keystroke (tea.KeyMsg), a window resize (tea.WindowSizeMsg),
//              a timer firing (tickMsg we define below), or the result of
//              an RPC call (walletMsg, txsMsg, etc.)
//
//   tea.Cmd  — a function that returns a Msg. Bubble Tea runs Cmds in
//              goroutines for us, so the TUI never blocks. When the Cmd
//              returns, its Msg is delivered back to Update.
//
//   Update(msg) returns (Model, Cmd) — the new state and a follow-up Cmd
//              to run (or nil for none). tea.Batch runs several Cmds
//              concurrently; tea.Tick schedules a Msg for the future.
//
// So a typical cycle looks like:
//   1. tickMsg arrives → Update returns (m, Batch(fetchWallet, fetchTxs,…))
//   2. fetchWallet runs in a goroutine, calls GetWalletInfo, returns
//      walletMsg{w, err}
//   3. Update receives walletMsg, stores m.wallet = w, returns (m, nil)
//   4. View renders the new Model
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
type txsMsg struct {
	resp SinceBlockResponse
	err  error
}
type addrsMsg struct {
	a   []ReceivedAddress
	err error
}
type validateMsg struct {
	v   ValidateAddress
	err error
}
type sendResultMsg struct {
	txid string
	err  error
}

// spinnerTickMsg fires on a fast timer (every spinnerInterval) while the
// refresh spinner is running. It is separate from tickMsg because the
// refresh interval is seconds and the spinner frame rate is ~10 Hz.
type spinnerTickMsg time.Time

// spinnerFrames is the Braille dot spinner used in the footer while
// RPC fetches are in flight. The set is 10 frames long so the spinner
// appears to rotate smoothly at spinnerInterval (100 ms per frame).
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 100 * time.Millisecond

// spinnerTickCmd schedules the next spinner frame. The spinner message
// handler checks m.inflight before scheduling another tick, so the
// spinner self-terminates once all fetches settle.
func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(t time.Time) tea.Msg { return spinnerTickMsg(t) })
}

// bumpInflight increments the inflight counter and, if the spinner was
// idle before the bump, returns a spinnerTickCmd to restart it. Callers
// append the returned Cmd (or nil) to their tea.Batch — tea.Batch
// silently drops nil, so passing it unconditionally is safe.
func (m *Model) bumpInflight(n int) tea.Cmd {
	var cmd tea.Cmd
	if m.inflight == 0 {
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
// fetchTxs fetches transaction deltas via listsinceblock. The cursor from
// the previous successful fetch is passed in; on the very first call it
// is the empty string and the daemon returns the full wallet history.
// target_confirms=6 matches our "confirmed" threshold so shallow txs
// keep getting refreshed confirmation counts on every tick.
func fetchTxs(rpc *RPCClient, lastBlock string) tea.Cmd {
	return func() tea.Msg {
		resp, err := rpc.ListSinceBlock(lastBlock, 6, true)
		return txsMsg{resp: resp, err: err}
	}
}

func fetchAddrs(rpc *RPCClient) tea.Cmd {
	return func() tea.Msg {
		a, err := rpc.ListReceivedByAddress()
		return addrsMsg{a, err}
	}
}

// refreshAllCmd fires all five fetches SEQUENTIALLY via tea.Sequence.
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
		fetchTxs(m.rpc, m.txsLastBlock),
		fetchAddrs(m.rpc),
	)
}

// refreshCoreCmd is the serialised 4-fetch batch used on every timer
// tick. Same rationale as refreshAllCmd — see its comment — but we
// deliberately omit fetchAddrs here because ticks are supposed to be
// lightweight; addresses refresh event-driven from the txsMsg handler
// when a genuinely new tx is detected.
func (m *Model) refreshCoreCmd() tea.Cmd {
	return tea.Sequence(
		fetchWallet(m.rpc),
		fetchChain(m.rpc),
		fetchStaking(m.rpc),
		fetchTxs(m.rpc, m.txsLastBlock),
	)
}

func validateAddr(rpc *RPCClient, addr string) tea.Cmd {
	return func() tea.Msg {
		v, err := rpc.ValidateAddress(addr)
		return validateMsg{v, err}
	}
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

// ---- Init / Update ----------------------------------------------------

// Init is called once when the program starts. Whatever Cmd it returns is
// the first action the runtime executes — we kick off the recurring tick,
// the initial RPC fetches, and the spinner loop (which will self-stop
// once all five fetches land because NewModel pre-seeded inflight=5).
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.tickCmd(), m.refreshAllCmd(), spinnerTickCmd())
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
		// Refresh wallet/chain/staking and tx deltas on every tick,
		// serialised via refreshCoreCmd so we only hold one RPC worker
		// thread at a time.
		spin := m.bumpInflight(4)
		return m, tea.Batch(m.tickCmd(), m.refreshCoreCmd(), spin)

	case spinnerTickMsg:
		// Advance the spinner frame only while something is actually
		// being fetched. Once inflight drops to 0 the spinner stops
		// scheduling follow-ups and the footer right-side goes blank.
		if m.inflight == 0 {
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
		if alreadyLoaded && hasNew {
			spin := m.bumpInflight(1)
			return m, tea.Batch(fetchAddrs(m.rpc), spin)
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
			m.addrCursor = clampCursor(m.addrCursor, len(m.addresses))
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
	case modeConfig:
		return m.handleConfigKey(msg)
	case modeTxDetail:
		// The detail modal is read-only: any of these keys closes it.
		if k := msg.String(); k == "esc" || k == "q" || k == "enter" {
			m.mode = modeDashboard
		}
		return m, nil
	}
	// Dashboard-mode keys.
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		// Anti-pileup: if a previous refresh is still running, ignore
		// the keystroke instead of stacking another 5-fetch sequence
		// behind it. The spinner already tells the user a refresh is
		// in progress.
		if m.inflight > 0 {
			return m, nil
		}
		spin := m.bumpInflight(5)
		return m, tea.Batch(m.refreshAllCmd(), spin)
	case "s":
		m.openSendModal()
		return m, nil
	case "c":
		m.openConfigModal()
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
		return m, nil
	case "enter":
		// Enter only opens the tx detail modal — pressing it while the
		// addresses panel is focused is a no-op on purpose.
		if m.focusedArea == focusTx && len(m.txs) > 0 && m.txCursor >= 0 && m.txCursor < len(m.txs) {
			m.mode = modeTxDetail
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
		return &m.addrCursor, len(m.addresses)
	}
	return &m.txCursor, len(m.txs)
}

// scrollBy moves the cursor of the currently-focused list by delta rows,
// clamped to [0, len-1]. Positive delta scrolls down.
func (m *Model) scrollBy(delta int) {
	cursor, length := m.focusedList()
	*cursor = clampCursor(*cursor+delta, length)
}

// scrollTo jumps the cursor of the currently-focused list to an absolute
// position. Negative values clamp to 0 and values past the end clamp to
// the last row — the caller is free to pass length-1 for "go to end".
func (m *Model) scrollTo(pos int) {
	cursor, length := m.focusedList()
	*cursor = clampCursor(pos, length)
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

// openSendModal resets the send wizard to step 0 and focuses the address
// field. It preserves the existing textinput.Model instances so their
// placeholder / width / mask settings survive.
func (m *Model) openSendModal() {
	m.mode = modeSend
	m.send = sendState{
		step:        sendStepAddress,
		address:     m.send.address,
		amount:      m.send.amount,
		passphrase:  m.send.passphrase,
		needsUnlock: m.wallet.UnlockedUntil != nil && *m.wallet.UnlockedUntil == 0,
	}
	m.send.address.SetValue("")
	m.send.amount.SetValue("")
	m.send.passphrase.SetValue("")
	m.send.address.Focus()
}

// handleSendKey is the send-wizard's input handler. It acts as a small
// state machine: the current m.send.step decides which keys do what.
func (m Model) handleSendKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.send.blurAll()
		return m, nil
	}
	if m.send.busy {
		return m, nil // ignore input while the final send RPC is in flight
	}
	switch m.send.step {
	case sendStepAddress:
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
	m.txs = nil
	m.txsLastBlock = "" // force a full re-seed against the new daemon
	m.addresses = nil
	m.walletErr = ""
	m.txsErr = ""
	m.addrsErr = ""
	m.mode = modeDashboard
	spin := m.bumpInflight(5)
	return m, tea.Batch(m.tickCmd(), m.refreshAllCmd(), spin)
}

// txKey is the composite uniqueness key for a Transaction entry. It has
// to distinguish entries from the same on-chain tx that differ in output
// — gridcoinresearchd emits one entry per (tx, vout, recipient) tuple,
// so hashing by txid alone would collapse multi-output transactions.
// We store the amount as fixed-point satoshis (1 GRC = 1e8 sat) instead
// of a raw float64 so two entries with "the same" amount always compare
// equal — float representations of decimal amounts can round-trip
// differently across RPC calls and defeat a naive ==.
type txKey struct {
	TxID     string
	Category string
	Address  string
	AmountSat int64
}

func makeTxKey(tx Transaction) txKey {
	return txKey{
		TxID:      tx.TxID,
		Category:  tx.Category,
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
