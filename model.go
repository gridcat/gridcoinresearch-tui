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
	modeConfig                    // the runtime config editor modal
	modeTxDetail                  // a modal showing one transaction in detail
)

// focusArea identifies which scrollable list on the dashboard is "active"
// — i.e. which one arrow keys / page keys / enter apply to. The user
// toggles between them with the tab key.
type focusArea int

const (
	focusTx focusArea = iota // transactions panel (default)
	focusAddr                // My Addresses panel
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

	// Modal sub-states. Only one modal is active at a time, but keeping
	// both fields means we preserve state if the user hits esc and comes
	// back.
	send sendState
	conf configState
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

	pass := textinput.New()
	pass.Placeholder = "wallet passphrase"
	// EchoMode = EchoPassword makes the textinput render each character as
	// the echo character below instead of the real keystroke.
	pass.EchoMode = textinput.EchoPassword
	pass.EchoCharacter = '•'
	pass.CharLimit = 128
	pass.Width = 40

	return Model{
		cfg: cfg,
		rpc: rpc,
		// Init will fire 5 fetches (wallet, chain, staking, txs, addrs)
		// right after Bubble Tea calls Init on us. Pre-seeding inflight
		// here means the spinner's first tick sees a positive counter
		// and doesn't immediately stop itself.
		inflight: 5,
		send:     sendState{address: addr, amount: amt, passphrase: pass},
		conf:     newConfigState(cfg),
	}
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
