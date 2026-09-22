package tui

import (
	"time"

	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/selfupdate"
	"github.com/gridcat/gridcoinresearch-tui/internal/telemetry"
)

// tickMsg fires every cfg.Refresh interval and drives the polling loop.
// The underlying time.Time is useful for ordering / debug logging.
type tickMsg time.Time

// One struct per RPC so we can tell in Update which fetch finished.
type walletMsg struct {
	w   rpc.WalletInfo
	err error
}

type chainMsg struct {
	c   rpc.BlockchainInfo
	err error
}

type stakingMsg struct {
	s   rpc.StakingInfo
	err error
}

type peersMsg struct {
	peers []rpc.PeerInfo
	err   error
}

// sharingTickMsg fires when it is time to send another peer report.
type sharingTickMsg time.Time

// sharingDoneMsg carries the result of one report. It deliberately does NOT
// touch m.inflight: the dashboard spinner is for RPC the user is waiting on,
// and a background courtesy must never make the footer flash "refreshing".
type sharingDoneMsg struct {
	ack telemetry.Ack
	err error
}

type txsMsg struct {
	resp rpc.SinceBlockResponse
	err  error
}

type addrsMsg struct {
	a   []rpc.ReceivedAddress
	err error
}

// addrMineMsg carries authoritative ownership flags resolved by
// validateaddress, keyed by address, to be merged into Model.addrMine.
type addrMineMsg struct {
	mine map[string]bool
}

// txContractsMsg carries Gridcoin contract types resolved by gettransaction,
// keyed by txid, to be merged into Model.txContracts. An entry with an empty
// value means "resolved, carries no contract". See fetchContracts.
type txContractsMsg struct {
	types map[string]string
}

type validateMsg struct {
	v   rpc.ValidateAddress
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
	v   rpc.ValidateAddress
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
	polls           []rpc.Poll
	err             error
}

// pollResultMsg carries one lazily-fetched getpollresults tally, tagged with
// the poll ID it belongs to so the handler can slot it into the cache even if
// the cursor has since moved on.
type pollResultMsg struct {
	id     string
	result rpc.PollResult
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
// state machine, so a background check that races with it, and especially a
// background *error*, can never flip the modal to a false "failed".
type updateCheckMsg struct {
	rel            selfupdate.Release
	missedReleases []selfupdate.Release
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

// spinnerTickMsg fires on a timer (every spinnerInterval) while the refresh
// spinner is running. It is separate from tickMsg because the refresh interval
// is seconds and the spinner frame rate is ~4 Hz.
type spinnerTickMsg time.Time
