// This file declares the Bubble Tea Model — the single struct that holds
// ALL of the TUI's state. Bubble Tea uses an Elm-inspired architecture:
//
//   • Model  — all state lives here (this file)
//   • Update — receives messages, returns a new Model and an optional Cmd
//              to run next. Defined in update.go.
//   • View   — renders the current Model to a string. Defined in view.go.
//
// The loop is: message in → Update returns (Model, Cmd) → View renders →
// next message arrives. No global variables, no hidden state. If you can't
// find a piece of state in this file, it doesn't exist.
package main

import (
	"time"

	// textinput is a small reusable component from the Bubble Tea ecosystem
	// that knows how to render a one-line input field and handle cursor
	// movement, backspace, paste, masking, etc.
	"github.com/charmbracelet/bubbles/textinput"
)

// viewMode identifies which screen is currently showing. Only one mode is
// active at a time; the View method dispatches on this value.
type viewMode int

const (
	modeDashboard viewMode = iota // the default full-screen dashboard
	modeSend                      // the "send GRC" wizard modal
	modeSign                      // the "sign message" wizard modal
	modeConfig                    // the runtime config editor modal
	modeTxDetail                  // a modal showing one transaction in detail
	modeEditLabel                 // the "edit address label" modal
	modeHelp                      // the keybinding / capability cheat sheet
)

// focusArea identifies which scrollable list on the dashboard is "active"
// — i.e. which one arrow keys / page keys / enter apply to. The user
// toggles between them with the tab key.
type focusArea int

const (
	focusTx focusArea = iota // transactions panel (default)
	focusAddr                // My Addresses panel
)

// addrTab identifies which ownership filter the My Addresses panel is showing.
// The user switches between them with the 1/2/3 keys. The zero value is
// addrTabMine, so the panel defaults to the user's own addresses.
type addrTab int

const (
	addrTabMine   addrTab = iota // own + not-yet-resolved addresses (default)
	addrTabOthers                // foreign addresses (labelled send targets)
	addrTabAll                   // the full address book
)

// configField is a type-safe enum for rows in the config modal. iota gives
// each constant a unique integer starting from 0, so they can be compared
// and used as array indices.
type configField int

const (
	cfgFieldNetwork configField = iota
	cfgFieldHost
	cfgFieldPort
	cfgFieldUser
	cfgFieldRefresh
	cfgFieldApply
	cfgFieldCount // sentinel: not a real field, used for modulo in tab navigation
)

// configState holds everything the config modal needs while it is open.
// Each editable row owns a textinput.Model (which handles cursor/typing),
// and errMsg is shown underneath the form when validation fails.
//
// Note: the RPC password is NOT editable here. It is shown as a read-only
// status line (see renderConfigModal) so shoulder-surfers can't reveal it
// and so the user doesn't have to re-type it to tweak unrelated fields.
type configState struct {
	focused configField
	testnet bool
	host    textinput.Model
	port    textinput.Model
	user    textinput.Model
	refresh textinput.Model
	errMsg  string
}

// sendStep is the state-machine step inside the send modal. The wizard
// walks the user through address → amount → (passphrase) → confirm →
// result.
type sendStep int

const (
	sendStepAddress    sendStep = iota // type + validate the recipient
	sendStepAmount                     // type the amount
	sendStepPassphrase                 // only used when the wallet is encrypted + locked
	sendStepConfirm                    // show "are you sure?"
	sendStepResult                     // show txid or error
)

// sendState is the live state of the send modal. amountValue is cached
// once the user leaves the amount step so the confirm view doesn't have
// to re-parse the string.
type sendState struct {
	step        sendStep
	address     textinput.Model
	amount      textinput.Model
	passphrase  textinput.Model
	amountValue float64 // parsed once when leaving the amount step
	needsUnlock bool    // true if the daemon says the wallet is currently locked
	validating  bool    // true while an address validateaddress RPC is in flight
	errMsg      string  // per-step validation / RPC error message
	busy        bool    // true while the final send command is running
	resultTxID  string  // populated in sendStepResult on success
	resultErr   string  // populated in sendStepResult on failure
}

// blurAll takes focus away from every input in the send state. Called when
// closing the modal so the cursor doesn't blink on an invisible field.
// Pointer receiver (*sendState) so mutations to the embedded textinputs
// actually stick.
func (s *sendState) blurAll() {
	s.address.Blur()
	s.amount.Blur()
	s.passphrase.Blur()
}

// signStep is the state-machine step inside the sign-message modal. The
// passphrase step is skipped entirely when the wallet is unencrypted or
// already unlocked — see openSignModal / runSign.
type signStep int

const (
	signStepAddress    signStep = iota // type / pre-fill the signing address
	signStepMessage                    // type the message to sign
	signStepPassphrase                 // only used when the wallet is encrypted + locked
	signStepResult                     // show signature or error
)

// signState is the live state of the sign-message modal. resultSig holds the
// base64 signature returned by signmessage on success.
type signState struct {
	step        signStep
	address     textinput.Model
	message     textinput.Model
	passphrase  textinput.Model
	needsUnlock bool   // true if the daemon says the wallet is currently locked
	busy        bool   // true while the signmessage RPC is running
	errMsg      string // per-step validation error
	resultSig   string // populated in signStepResult on success
	resultErr   string // populated in signStepResult on failure
}

func (s *signState) blurAll() {
	s.address.Blur()
	s.message.Blur()
	s.passphrase.Blur()
}

// editLabelState is the live state of the edit-label modal. There is a single
// text input and no multi-step wizard (setting a label needs no passphrase),
// so it carries none of the step/needsUnlock/result machinery the send and
// sign modals do — just the input, the target address, an in-flight flag, and
// an error string. On success the modal closes outright, so there is no result
// field to populate.
type editLabelState struct {
	label   textinput.Model // editable label text (empty = clear the label)
	address string          // address whose label we're editing (read-only display)
	busy    bool            // true while the setaccount RPC is in flight
	errMsg  string          // RPC / validation error shown under the input
}

func (s *editLabelState) blurAll() {
	s.label.Blur()
}

func (cs *configState) blurAll() {
	cs.host.Blur()
	cs.port.Blur()
	cs.user.Blur()
	cs.refresh.Blur()
}

// inputFor maps a configField enum to the pointer of the matching text
// input so the dispatch switch in update.go becomes a single line instead
// of a five-way copy-paste. Returns nil for rows that are not editable
// text inputs (Network toggle and Apply button), which is a signal to the
// caller to handle them specially.
func (cs *configState) inputFor(f configField) *textinput.Model {
	switch f {
	case cfgFieldHost:
		return &cs.host
	case cfgFieldPort:
		return &cs.port
	case cfgFieldUser:
		return &cs.user
	case cfgFieldRefresh:
		return &cs.refresh
	}
	return nil
}

// Model is THE big state struct. Bubble Tea's program loop takes a Model,
// calls Update on it for each incoming message, and calls View on it to
// render a frame. Everything the UI displays — RPC data, modal state,
// scroll positions, error strings, window dimensions — lives here.
type Model struct {
	cfg    Config
	rpc    *RPCClient
	width  int // current terminal width in columns  (updated by tea.WindowSizeMsg)
	height int // current terminal height in rows    (updated by tea.WindowSizeMsg)
	mode   viewMode

	// Data cached from the most recent RPC responses.
	wallet     WalletInfo
	chain      BlockchainInfo
	staking    StakingInfo
	txs        []Transaction
	addresses  []ReceivedAddress
	lastUpdate time.Time
	// addrMine caches authoritative per-address ownership (validateaddress
	// ismine). listreceivedbyaddress returns the entire address book —
	// foreign addresses you've merely labelled included — so the My
	// Addresses panel can't tell which entries are actually yours without
	// this. A missing key means "not resolved yet". See fetchAddrOwnership.
	addrMine map[string]bool

	// txsLastBlock is the "lastblock" cursor returned by the previous
	// listsinceblock call. Empty on first launch — an empty cursor tells
	// the daemon to return the full wallet history. After the first
	// response we always have a real blockhash to delta-fetch from.
	txsLastBlock string

	// Per-source errors. We track them separately instead of a single
	// lastErr field because the fetches happen in parallel every tick —
	// a global error field gets clobbered by the next successful call and
	// the user never sees the real failure.
	walletErr string
	txsErr    string
	addrsErr  string

	// Loaded-at-least-once flags so panels can switch from "loading…" to
	// the real (or error) content after the first RPC round-trip.
	loaded      bool
	txsLoaded   bool
	addrsLoaded bool

	// inflight is the number of RPC fetches currently running. The
	// footer shows a spinner whenever this is non-zero; when it drops
	// back to 0 the spinner self-stops.
	inflight int
	// spinnerRunning is true while a spinner tick chain is live. It guards
	// bumpInflight so a burst of back-to-back fetches (each briefly dropping
	// inflight to 0 and back) can't spawn overlapping spinner timer
	// goroutines. Set when the chain starts, cleared when a tick finds
	// inflight == 0 and stops.
	spinnerRunning bool
	// spinnerFrame advances on every spinner tick so the footer can cycle
	// through the frames in update.go's spinnerFrames array.
	spinnerFrame int

	// Which scrollable panel the arrow/page keys drive.
	focusedArea focusArea

	// Cursors for the two scrollable lists. The "offset" (top-of-window
	// index) is intentionally NOT stored on the Model — render functions
	// receive m by value, so any write to an offset field would be
	// discarded on return anyway. Each render recomputes the offset
	// deterministically from the current cursor and the available rows.
	txCursor   int
	addrCursor int

	// addrHScroll is the horizontal column offset for the My Addresses panel,
	// driven by left/right when that panel is focused. Lets long rows (address
	// + label + amount) pan sideways instead of wrapping. Reset to 0 whenever
	// focus leaves the panel so re-entering always starts at the left edge.
	addrHScroll int

	// addrPanelRows is the user's chosen height (in rows) for the My Addresses
	// panel, set by the +/-/0 resize keys. 0 means "auto" — fall back to the
	// computed default (see addrPanelHeight). Session-only; never persisted.
	addrPanelRows int

	// addrTab is the ownership filter the My Addresses panel currently shows,
	// switched with the 1/2/3 keys. See visibleAddresses.
	addrTab addrTab

	// anonymous hides monetary amounts on screen. Toggled at runtime via
	// the "a" hotkey so the user can safely show the dashboard in public.
	anonymous bool

	// Modal sub-states. Only one modal is active at a time, but keeping
	// both fields means we preserve state if the user hits esc and comes
	// back.
	send sendState
	sign signState
	conf configState
	edit editLabelState
}

// NewModel constructs the initial Model. This is where the one-time setup
// of the textinput components happens (placeholder, character limit, echo
// mode for the password field).
func NewModel(cfg Config, rpc *RPCClient) Model {
	addr := textinput.New()
	addr.Placeholder = "S-address"
	addr.CharLimit = 64
	addr.Width = 50

	amt := textinput.New()
	amt.Placeholder = "0.00"
	amt.CharLimit = 20
	amt.Width = 20

	signAddr := textinput.New()
	signAddr.Placeholder = "S-address (one of your wallet's addresses)"
	signAddr.CharLimit = 64
	signAddr.Width = 50

	signMsg := textinput.New()
	signMsg.Placeholder = "message to sign"
	signMsg.CharLimit = 1024
	signMsg.Width = 50

	labelInput := textinput.New()
	labelInput.Placeholder = "label (empty to clear)"
	labelInput.CharLimit = 128
	labelInput.Width = 50

	return Model{
		cfg: cfg,
		rpc: rpc,
		// Init will fire 5 fetches (wallet, chain, staking, txs, addrs)
		// right after Bubble Tea calls Init on us. Pre-seeding inflight
		// here means the spinner's first tick sees a positive counter
		// and doesn't immediately stop itself. Init starts that spinner
		// chain directly, so mark it running to keep bumpInflight's guard
		// honest from the first frame.
		inflight:       5,
		spinnerRunning: true,
		addrMine:       make(map[string]bool),
		send:           sendState{address: addr, amount: amt, passphrase: newPassphraseInput()},
		sign:           signState{address: signAddr, message: signMsg, passphrase: newPassphraseInput()},
		conf:           newConfigState(cfg),
		edit:           editLabelState{label: labelInput},
	}
}

// newPassphraseInput builds a fresh masked textinput for any wallet
// passphrase prompt. EchoMode = EchoPassword makes the textinput render
// each character as the echo character instead of the real keystroke, so
// the passphrase never lands on screen.
func newPassphraseInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "wallet passphrase"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 128
	ti.Width = 40
	return ti
}

// addrOwnership is the resolved ownership of an address shown in the My
// Addresses panel. listreceivedbyaddress returns the whole address book, so a
// labelled foreign address (a send target) appears there looking just like
// one of your own — the danger the issue tracker flags. validateaddress tells
// the two apart; until it has, we say nothing rather than imply ownership.
type addrOwnership int

const (
	ownUnknown addrOwnership = iota // validateaddress hasn't resolved it yet
	ownMine                         // validateaddress reported ismine
	ownForeign                      // validateaddress reported NOT ismine
)

// ownership reports whether addr is one of the wallet's own addresses,
// reading the cache populated by fetchAddrOwnership.
func (m Model) ownership(addr string) addrOwnership {
	mine, ok := m.addrMine[addr]
	switch {
	case !ok:
		return ownUnknown
	case mine:
		return ownMine
	default:
		return ownForeign
	}
}

// unknownOwnership returns the currently shown addresses whose ownership
// hasn't been resolved yet, so callers can validate just those.
func (m Model) unknownOwnership() []string {
	var out []string
	for _, a := range m.addresses {
		if _, ok := m.addrMine[a.Address]; !ok {
			out = append(out, a.Address)
		}
	}
	return out
}

// visibleAddresses returns the addresses the panel should show under the active
// tab. The partition is gap-free (Mine ∪ Others = All): Mine keeps everything
// that isn't known-foreign (owned, or not yet resolved), so freshly loaded rows
// appear immediately and only drop out if validateaddress later flags them
// foreign. Others keeps exactly the foreign ones. All returns the full slice
// untouched. The cursor, scroll, sign, and edit paths all read this, so the
// filter lives in one place.
func (m Model) visibleAddresses() []ReceivedAddress {
	if m.addrTab == addrTabAll {
		return m.addresses
	}
	wantForeign := m.addrTab == addrTabOthers // else addrTabMine: keep non-foreign
	var out []ReceivedAddress
	for _, a := range m.addresses {
		if (m.ownership(a.Address) == ownForeign) == wantForeign {
			out = append(out, a)
		}
	}
	return out
}

// selectedAddress returns the address highlighted in the My Addresses panel —
// the addrCursor row of the active tab — or nil when the tab is empty or the
// cursor is somehow out of range. Centralizing the cursor-into-filtered-list
// bounds check here means callers (the edit/sign entry points) don't each
// re-derive visibleAddresses() and re-apply the same guard.
func (m Model) selectedAddress() *ReceivedAddress {
	visible := m.visibleAddresses()
	if m.addrCursor < 0 || m.addrCursor >= len(visible) {
		return nil
	}
	return &visible[m.addrCursor]
}

// addrTabCounts returns the per-tab entry counts for the tab bar. others counts
// the known-foreign addresses; mine is everything else (own + unknown), which
// matches the visibleAddresses partition.
func (m Model) addrTabCounts() (mine, others, all int) {
	all = len(m.addresses)
	for _, a := range m.addresses {
		if m.ownership(a.Address) == ownForeign {
			others++
		}
	}
	mine = all - others
	return
}

// newConfigState builds a fresh configState pre-populated with the values
// currently in the live Config. Used both for the initial Model and for
// resetting the form each time the config modal is opened.
func newConfigState(cfg Config) configState {
	mk := func(value string, width int) textinput.Model {
		ti := textinput.New()
		ti.SetValue(value)
		ti.CharLimit = 128
		ti.Width = width
		return ti
	}
	return configState{
		testnet: cfg.Testnet,
		host:    mk(cfg.Host, 30),
		port:    mk(cfg.Port, 10),
		user:    mk(cfg.User, 30),
		refresh: mk(cfg.Refresh.String(), 10),
	}
}
