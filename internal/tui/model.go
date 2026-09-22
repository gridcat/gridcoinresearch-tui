// This file declares the Bubble Tea Model, the single struct that holds
// ALL of the TUI's state. Bubble Tea uses an Elm-inspired architecture:
//
//   - Model:  all state lives here (this file)
//   - Update: receives messages, returns a new Model and an optional Cmd
//     to run next. Defined in app_update.go.
//   - View:   renders the current Model to a string. Defined in app_view.go.
//
// The loop is: message in → Update returns (Model, Cmd) → View renders →
// next message arrives. No global variables, no hidden state. If you can't
// find a piece of state in this file, it doesn't exist.
package tui

import (
	"time"

	// textinput is a small reusable component from the Bubble Tea ecosystem
	// that knows how to render a one-line input field and handle cursor
	// movement, backspace, paste, masking, etc.
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/gridcat/gridcoinresearch-tui/internal/config"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/state"
	"github.com/gridcat/gridcoinresearch-tui/internal/telemetry"
)

// viewMode identifies which screen is currently showing. Only one mode is
// active at a time; the View method dispatches on this value.
type viewMode int

const (
	modeDashboard  viewMode = iota // the default full-screen dashboard
	modeSend                       // the "send GRC" wizard modal
	modeSign                       // the "sign message" wizard modal
	modeConfig                     // the runtime config editor modal
	modeTxDetail                   // a modal showing one transaction in detail
	modeEditLabel                  // the "edit address label" modal
	modeAddLabel                   // the "add address label" modal
	modeHelp                       // the keybinding / capability cheat sheet
	modePolls                      // the full-screen governance polls list
	modePollDetail                 // a modal showing one poll in full (opened from modePolls)
	modeUpdate                     // the self-update modal (check → confirm → install)
	modeConsent                    // the one-time peer-sharing consent screen
)

// Model is THE big state struct. Bubble Tea's program loop takes a Model,
// calls Update on it for each incoming message, and calls View on it to
// render a frame. Everything the UI displays lives here: RPC data, modal
// state, scroll positions, error strings, window dimensions.
type Model struct {
	cfg    config.Config
	rpc    *rpc.Client
	width  int // current terminal width in columns  (updated by tea.WindowSizeMsg)
	height int // current terminal height in rows    (updated by tea.WindowSizeMsg)
	mode   viewMode

	// Data cached from the most recent RPC responses.
	wallet     rpc.WalletInfo
	chain      rpc.BlockchainInfo
	staking    rpc.StakingInfo
	txs        []rpc.Transaction
	addresses  []rpc.ReceivedAddress
	lastUpdate time.Time
	// Peer-connection counts derived from getpeerinfo (total = in + out).
	// peersLoaded distinguishes "0 peers" (worth a warning badge) from
	// "not fetched yet" (render nothing).
	peersTotal  int
	peersIn     int
	peersOut    int
	peersLoaded bool
	// peers holds the last getpeerinfo result, kept only so a peer-sharing
	// tick has something to report without issuing its own RPC. It is
	// discarded like any other cached fetch; nothing reads it unless sharing
	// is on.
	peers []rpc.PeerInfo

	// ---- peer sharing -------------------------------------------------
	// state is the on-disk answer (see state.go). cfg.PeerSharing overrides
	// it for this launch when a flag or env var was given.
	state state.State
	// sharingOn is the resolved answer actually in force right now.
	sharingOn bool
	// sharingInterval is how long until the next report; the collector can
	// widen or narrow it per response.
	sharingInterval time.Duration
	// sharingNote is a short one-line result shown in the footer, e.g. after
	// a failed post. Never a modal: sharing is a background courtesy and must
	// not interrupt anyone.
	sharingNote string
	// nextReportAt is when the next report falls due. The schedule lives here
	// as data rather than in the timer because a tea.Tick already in flight
	// cannot be re-timed or cancelled: holding it here is what lets the
	// collector's next_report_after apply to the very next report instead of
	// the one after it. Zero means nothing is scheduled.
	nextReportAt time.Time
	// addrMine caches authoritative per-address ownership (validateaddress
	// ismine). listreceivedbyaddress returns the entire address book,
	// including foreign addresses you've merely labelled, so the My
	// Addresses panel can't tell which entries are actually yours without
	// this. A missing key means "not resolved yet". See fetchAddrOwnership.
	addrMine map[string]bool

	// txContracts caches the Gridcoin contract type of a transaction, keyed
	// by txid, because listsinceblock can't tell us (see
	// IsContractCandidate). Three states, all meaningful: a missing key is
	// "not looked up yet", an empty string is "looked up, carries no
	// contract", and anything else is the type ("beacon", "vote", …).
	// Contracts are immutable once mined, so a resolved entry never needs
	// refreshing. See fetchContracts.
	txContracts map[string]string

	// txsLastBlock is the "lastblock" cursor returned by the previous
	// listsinceblock call. Empty on first launch, and an empty cursor tells
	// the daemon to return the full wallet history. After the first
	// response we always have a real blockhash to delta-fetch from.
	txsLastBlock string

	// Per-source errors. We track them separately instead of a single
	// lastErr field because the fetches happen in parallel every tick. A
	// global error field gets clobbered by the next successful call and the
	// user never sees the real failure.
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
	// through the frames in ui.SpinnerFrames.
	spinnerFrame int

	// Which scrollable panel the arrow/page keys drive.
	focusedArea focusArea

	// Cursors for the two scrollable lists. txOffset is the first visible
	// transaction. It is stored so, after moving down through a long list,
	// moving up first moves the cursor up the already visible rows before the
	// list itself scrolls.
	txCursor   int
	txOffset   int
	addrCursor int

	// addrHScroll is the horizontal column offset for the My Addresses panel,
	// driven by left/right when that panel is focused. Lets long rows (address
	// + label + amount) pan sideways instead of wrapping. Reset to 0 whenever
	// focus leaves the panel so re-entering always starts at the left edge.
	addrHScroll int

	// addrPanelRows is the user's chosen height (in rows) for the My Addresses
	// panel, set by the +/-/0 resize keys. 0 means "auto": fall back to the
	// computed default (see addrPanelHeight). Session-only; never persisted.
	addrPanelRows int

	// addrTab is the ownership filter the My Addresses panel currently shows,
	// switched with the 1/2/3 keys. See visibleAddresses.
	addrTab addrTab

	// addrSearch is the inline label/address filter for the address panel. A
	// non-empty value keeps filtering after the input loses focus; Focused
	// distinguishes actively typing from navigating the filtered rows.
	addrSearch textinput.Model

	// anonymous hides monetary amounts on screen. Toggled at runtime via
	// the "a" hotkey so the user can safely show the dashboard in public.
	anonymous bool

	// ---- Polls (governance) --------------------------------------------
	// Populated lazily when the polls screen is opened with "p", and never on
	// the refresh tick, because listpolls (and especially the per-poll
	// getpollresults tally) is heavier than the dashboard fetches.
	polls       []rpc.Poll
	pollCursor  int
	pollsLoaded bool
	pollsErr    string
	// pollsShowFinished mirrors listpolls's `showfinished` argument. Defaults
	// to true ("all polls"); the tab key toggles it to false (active only).
	pollsShowFinished bool
	// pollResults caches getpollresults tallies keyed by poll ID, filled in
	// lazily for the poll under the cursor (participation % + leading answer).
	// pollResultPending guards against firing a second tally while one is in
	// flight. pollResultErr records a failed tally so the detail popup can show
	// why (rather than a perpetual "tallying…") and offer a retry; a poll is in
	// exactly one of these three states, or none before it's ever fetched.
	pollResults       map[string]rpc.PollResult
	pollResultPending map[string]bool
	pollResultErr     map[string]string

	// ---- Self-update ---------------------------------------------------
	// updateAvailable + latestVersion drive the header badge; they're set by
	// the silent background check and refreshed whenever the update modal runs
	// its own check. update holds the modal's own state machine.
	updateAvailable bool
	latestVersion   string // newest release tag, "v" stripped, for display
	update          updateState
	// restartExe is set to the on-disk path of the freshly-installed binary
	// when a self-update succeeds. main.go reads it after the TUI exits and
	// re-execs that path so the user lands back in the running app.
	restartExe string

	// Modal sub-states. Only one modal is active at a time, but keeping
	// both fields means we preserve state if the user hits esc and comes
	// back.
	send sendState
	sign signState
	conf configState
	edit editLabelState
	add  addLabelState
}

// NewModel constructs the initial Model. This is where the one-time setup
// of the textinput components happens (placeholder, character limit, echo
// mode for the password field).
func NewModel(cfg config.Config, c *rpc.Client) Model {
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

	addAddress := textinput.New()
	addAddress.Placeholder = "S-address"
	addAddress.CharLimit = 64
	addAddress.Width = 50

	addLabel := textinput.New()
	addLabel.Placeholder = "label"
	addLabel.CharLimit = 128
	addLabel.Width = 50

	addrSearch := textinput.New()
	addrSearch.Placeholder = "label or address"
	addrSearch.CharLimit = 128
	addrSearch.Width = 36

	// Resolve peer sharing once, here, so the rest of the program reads a
	// single boolean instead of re-deriving the precedence rule. A flag or
	// env value wins for this launch; otherwise the stored answer decides;
	// and if neither says anything the user has never been asked, which Init
	// turns into the consent screen.
	st := state.Load()
	sharing := st.PeerSharing
	if cfg.PeerSharing != state.PeerSharingUnset {
		sharing = cfg.PeerSharing
	}
	// A flag or env opt-in stores no consent answer and therefore has no
	// stored identifier on a fresh install, which would leave sharing switched
	// on but every report silently dropped. Mint one now so the setting means
	// what it says.
	if sharing == state.PeerSharingOn {
		st = state.EnsureReporterID(st)
	}

	// Never asked, and nothing on the command line answered for them. Open on
	// the consent screen so the very first thing the user sees is the choice,
	// rather than discovering the feature later in a settings panel.
	startMode := modeDashboard
	if st.PeerSharing == state.PeerSharingUnset && cfg.PeerSharing == state.PeerSharingUnset {
		startMode = modeConsent
	}

	return Model{
		cfg:             cfg,
		rpc:             c,
		mode:            startMode,
		state:           st,
		sharingOn:       sharing == state.PeerSharingOn,
		sharingInterval: telemetry.Interval,
		// Init will fire 6 fetches (wallet, chain, staking, peers, txs,
		// addrs) right after Bubble Tea calls Init on us. Pre-seeding
		// inflight here means the spinner's first tick sees a positive
		// counter and doesn't immediately stop itself. Init starts that
		// spinner chain directly, so mark it running to keep
		// bumpInflight's guard honest from the first frame.
		inflight:       6,
		spinnerRunning: true,
		addrMine:       make(map[string]bool),
		txContracts:    make(map[string]string),
		// Default the polls screen to "all polls" (incl. finished); tab narrows
		// it to active only. The lazy-tally caches start empty.
		pollsShowFinished: true,
		pollResults:       make(map[string]rpc.PollResult),
		pollResultPending: make(map[string]bool),
		pollResultErr:     make(map[string]string),
		send:              sendState{address: addr, amount: amt, passphrase: newPassphraseInput()},
		sign:              signState{address: signAddr, message: signMsg, passphrase: newPassphraseInput()},
		conf:              newConfigState(cfg, sharing == state.PeerSharingOn),
		edit:              editLabelState{label: labelInput},
		add:               addLabelState{address: addAddress, label: addLabel},
		addrSearch:        addrSearch,
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
