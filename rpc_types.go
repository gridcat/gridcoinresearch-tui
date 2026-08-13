// This file declares Go structs that mirror the JSON shapes returned by the
// gridcoinresearchd RPC. We deliberately only list the fields the TUI
// actually renders — encoding/json silently ignores fields the daemon sends
// that we don't have in the struct, which keeps these definitions tiny.
//
// Every field tagged with `json:"name"` is saying "pull the JSON key called
// `name` into this Go field". The Go side uses CamelCase, JSON uses
// lowercase — the tag bridges the two conventions.
package main

import "encoding/json"

// WalletInfo matches the response of the getwalletinfo RPC. All amounts are
// in GRC (floats), not satoshis.
type WalletInfo struct {
	Balance            float64 `json:"balance"`
	UnconfirmedBalance float64 `json:"unconfirmed_balance"`
	ImmatureBalance    float64 `json:"immature_balance"`
	NewMint            float64 `json:"newmint"`
	Stake              float64 `json:"stake"`
	TxCount            int     `json:"txcount"`
	// UnlockedUntil is a POINTER to int64 instead of a plain int64 so we can
	// distinguish three cases that the daemon collapses into one field:
	//   nil              — the field wasn't in the response (unencrypted wallet)
	//   *v == 0          — the wallet is encrypted and currently locked
	//   *v > 0           — unix timestamp when the wallet will auto-relock
	// A plain int64 can't express "absent" because its zero value is 0, which
	// would collide with "locked".
	UnlockedUntil *int64 `json:"unlocked_until"`
}

// IsLocked reports whether the wallet currently needs a passphrase before
// it will sign or send. True only when the wallet is encrypted AND not
// currently unlocked — an unencrypted wallet (UnlockedUntil == nil) and
// one the user has already unlocked themselves (e.g. for staking) both
// return false, so callers don't ask for a passphrase the daemon doesn't
// need.
func (w WalletInfo) IsLocked() bool {
	return w.UnlockedUntil != nil && *w.UnlockedUntil == 0
}

// BlockchainInfo matches getblockchaininfo. We only use Chain and Blocks in
// the UI today; the rest is kept for future use.
type BlockchainInfo struct {
	Chain                string  `json:"chain"` // "main" or "test"
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
}

// PeerInfo is one entry from getpeerinfo. The daemon returns one object per
// connected peer with plenty of detail (address, version, ping…); the TUI
// only needs the Inbound flag to split the header's peer count into
// inbound/outbound, so per the file convention that's all we decode.
type PeerInfo struct {
	Inbound bool `json:"inbound"`
}

// StakingInfo matches getstakinginfo. Field tags that look weird (with
// dashes) match Gridcoin's actual JSON keys — gridcoinresearchd uses
// "mining-error" and "time-to-stake_days" rather than the camelCase you
// might expect. Difficulty is a custom type, see StakingDifficulty below.
type StakingInfo struct {
	Enabled      bool              `json:"enabled"`
	Staking      bool              `json:"staking"`
	MiningError  string            `json:"mining-error"`
	Difficulty   StakingDifficulty `json:"difficulty"`
	NetStakeWt   float64           `json:"netstakeweight"`
	ExpectedTime int64             `json:"expectedtime"` // seconds until the wallet expects to stake next

	// Researcher-only fields: the daemon includes these when a CPID is
	// configured (crunchers) and omits them entirely for investors —
	// pointers so "no CPID" (nil) is distinguishable from a real 0.
	Magnitude     *float64 `json:"current_magnitude"`
	PendingReward *float64 `json:"BoincRewardPending"`

	// CPID is always present, unlike the two above: a 32-char hex digest for
	// a cruncher, or a short placeholder word for everyone else. A plain
	// string (not a pointer) because there is no absent-vs-zero ambiguity to
	// preserve — every non-cruncher value means the same thing. See
	// IsCruncher for why we never compare it against a literal.
	CPID string `json:"CPID"`
}

// IsCruncher reports whether this wallet has a BOINC CPID (as opposed to
// being investor-only).
//
// The length check is the whole test, deliberately. The daemon builds this
// field from MiningId::ToString(), which returns a 32-char hex digest for a
// real CPID and otherwise one of several short placeholders — "NONCRUNCHER"
// in the 5.5.0 source, "INVESTOR" per older reports, or "" when unset. A real
// CPID is always exactly 32 hex chars, so a length test recognises the
// cruncher case without us having to keep a list of the daemon's
// ever-shifting placeholder words in sync.
//
// Note a CPID can exist without an active beacon (magnitude would then be 0),
// so this answers "is there a CPID", not "is this wallet earning".
func (s StakingInfo) IsCruncher() bool {
	return len(s.CPID) == 32
}

// StakingDifficulty is the awkward case. Depending on the wallet version,
// the `difficulty` field in getstakinginfo can arrive either as:
//
//	"difficulty": 0.1234
//
// or as a nested object:
//
//	"difficulty": {"proof-of-stake": 0.1234, "proof-of-work": 0, "current": 0.1234}
//
// Gridcoin uses the object form. We need to handle both so the client works
// against any wallet version — that's what the custom UnmarshalJSON below does.
type StakingDifficulty struct {
	ProofOfStake float64 `json:"proof-of-stake"`
	ProofOfWork  float64 `json:"proof-of-work"`
	Current      float64 `json:"current"`
}

// UnmarshalJSON is a special method name recognised by encoding/json: if a
// type has one, json.Unmarshal hands it the raw bytes and lets it decide how
// to populate itself. This is how we support "difficulty can be either a
// number or an object" without making the rest of the code care.
//
// Strategy: try decoding as a plain float first. If that works, we're
// talking to an older bitcoin-style wallet; store the value in ProofOfStake.
// If the float decode fails, assume the object form and decode into an alias
// type so we don't recurse back into this same UnmarshalJSON method.
func (d *StakingDifficulty) UnmarshalJSON(data []byte) error {
	var f float64
	if err := json.Unmarshal(data, &f); err == nil {
		d.ProofOfStake = f
		return nil
	}
	// `type raw StakingDifficulty` creates an alias WITHOUT the UnmarshalJSON
	// method attached. Decoding into `raw` uses the default struct-tag
	// behaviour instead of infinitely recursing back into our custom method.
	type raw StakingDifficulty
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*d = StakingDifficulty(r)
	return nil
}

// Value returns the single most meaningful difficulty number for display.
// Gridcoin is a pure Proof-of-Stake chain so the proof-of-stake sub-field is
// what the user actually cares about; the other branches are fallbacks for
// older wallet versions or unexpected response shapes.
func (d StakingDifficulty) Value() float64 {
	if d.ProofOfStake > 0 {
		return d.ProofOfStake
	}
	if d.Current > 0 {
		return d.Current
	}
	return d.ProofOfWork
}

// SinceBlockResponse matches the envelope returned by listsinceblock:
// every wallet transaction that is either unconfirmed or has confirmed
// after the given block, plus a "lastblock" cursor the caller passes back
// on the next call to fetch only the delta from there.
type SinceBlockResponse struct {
	Transactions []Transaction `json:"transactions"`
	LastBlock    string        `json:"lastblock"`
}

// Transaction is a single entry from listtransactions / listsinceblock.
// Category is a stringly-typed enum on the wire; we use it raw here and
// classify into a friendlier status bucket in format.go::ClassifyTransaction.
type Transaction struct {
	Category      string  `json:"category"` // "send" | "receive" | "generate" | "immature" | "move"
	Amount        float64 `json:"amount"`
	Fee           float64 `json:"fee"`
	Confirmations int64   `json:"confirmations"`
	Address       string  `json:"address"`
	TxID          string  `json:"txid"`
	Time          int64   `json:"time"`      // unix seconds
	BlockHash     string  `json:"blockhash"` // empty until the tx is mined
	BlockTime     int64   `json:"blocktime"` // unix seconds, empty until mined
	Comment       string  `json:"comment"`
}

// TxDetail is the slice of gettransaction we care about. Gridcoin's wallet
// gettransaction — unlike Bitcoin's — also runs the raw transaction through
// TxToJSON, so it carries the decoded Gridcoin contracts. That makes it the
// only wallet RPC that can tell us a "send" was really a beacon or a vote:
// listsinceblock never looks at a transaction's contracts at all.
//
// Everything else in the response (amount, fee, confirmations, vin/vout, the
// details array) we already have from listsinceblock, so we don't decode it.
// The response also repeats txid/time/blockhash twice, a quirk of building it
// from two different serialisers; encoding/json simply keeps the last of a
// duplicated key.
type TxDetail struct {
	Contracts []TxContract `json:"contracts"`
}

// TxContract is one Gridcoin contract attached to a transaction.
//
// We decode the type and nothing else. The sibling "body" field is
// polymorphic — an object whose shape differs per type, and a bare JSON
// string for type "message" — so decoding it would mean a RawMessage plus a
// per-type switch, and no part of the UI asks for its contents. "action"
// (add/remove) and "version" are skipped for the same reason.
type TxContract struct {
	// Type is the contract kind: "beacon", "vote", "poll", "project",
	// "message", "sidestake", "claim", "mrc", "protocol", "scraper", or ""
	// when the daemon itself could not classify it.
	Type string `json:"type"`
}

// ValidateAddress is the response of the validateaddress RPC. We only need
// IsValid for the send-flow pre-flight check.
type ValidateAddress struct {
	IsValid bool   `json:"isvalid"`
	Address string `json:"address"`
	IsMine  bool   `json:"ismine"`
}

// ReceivedAddress is one entry from listreceivedbyaddress. Gridcoin still
// emits the legacy `account` field instead of the newer `label` field, so
// we decode both and fall back in DisplayLabel.
//
// InvolvesWatchonly is true for addresses imported via importaddress
// without the private key — the daemon tracks balances on them but
// cannot sign on their behalf. The field is only present in the JSON
// when true (bitcoin-core convention), so a missing field correctly
// decodes to the default `false` and means "wallet owns the key".
type ReceivedAddress struct {
	Address           string  `json:"address"`
	Amount            float64 `json:"amount"`
	Confirmations     int64   `json:"confirmations"`
	Label             string  `json:"label"`             // newer bitcoin-core style
	Account           string  `json:"account"`           // legacy field still emitted by gridcoinresearchd
	InvolvesWatchonly bool    `json:"involvesWatchonly"` // true when the wallet only watches this address (no private key)
}

// DisplayLabel returns the label to show next to the address in the UI,
// preferring the newer field if both happen to be present.
func (r ReceivedAddress) DisplayLabel() string {
	if r.Label != "" {
		return r.Label
	}
	return r.Account
}

// Poll is one entry from the listpolls RPC (governance / voting). Following the
// file convention we decode only the fields the TUI renders: the list row
// (Title/WeightType/Expiration/Votes) plus the detail popup
// (Question/URL/ResponseType/DurationDays/Timestamp). The per-choice labels the
// popup's results breakdown shows come from the tally's responses[] (see
// PollResult), not from the poll's own choices[], so choices[] stays undecoded.
// Two things about the wire shape are worth knowing:
//
//   - Expiration and Timestamp arrive as PRE-FORMATTED "MM-DD-YYYY HH:MM:SS"
//     UTC strings (gridcoinresearchd's TimestampToHRDate), NOT unix ints — see
//     ParsePollTime / FormatPollTimeLeft in format.go.
//   - Votes is just the raw count of votes cast. The participation percentage
//     and the leading answer are NOT part of listpolls; they come from the
//     heavier getpollresults tally (see PollResult), fetched lazily per poll.
type Poll struct {
	Title        string `json:"title"`
	ID           string `json:"id"`
	Question     string `json:"question"`
	URL          string `json:"url"`
	WeightType   string `json:"weight_type"`   // "Magnitude" | "Balance" | "Magnitude+Balance" | ...
	ResponseType string `json:"response_type"` // "Yes/No/Abstain" | "Single Choice" | "Multiple Choice"
	DurationDays int    `json:"duration_days"`
	Timestamp    string `json:"timestamp"`  // when the poll was created, "MM-DD-YYYY HH:MM:SS" UTC
	Expiration   string `json:"expiration"` // "MM-DD-YYYY HH:MM:SS" UTC
	Votes        int    `json:"votes"`      // number of votes cast
}

// PollResult is the getpollresults response for a single poll — the heavy
// tally the daemon computes by walking every vote. We fetch it lazily for the
// poll under the cursor, never in the refresh loop. VotePercentAVW is a pointer
// because the daemon omits it before any vote, so "absent" is distinguishable
// from a legitimate zero; top_choice arrives as JSON null in that same case,
// which decodes to the empty string. Responses drives the detail popup's
// per-choice breakdown (share of TotalWeight).
type PollResult struct {
	VotePercentAVW *float64       `json:"vote_percent_avw"`
	TopChoice      string         `json:"top_choice"`
	TotalWeight    float64        `json:"total_weight"`
	Responses      []PollResponse `json:"responses"`
}

// PollResponse is one per-choice tally inside PollResult.Responses. Votes is a
// float, not an int: in a multiple-choice poll a voter can split one vote
// across choices, so the daemon reports fractional per-choice counts (e.g. 153.5).
type PollResponse struct {
	Choice string  `json:"choice"`
	Weight float64 `json:"weight"`
	Votes  float64 `json:"votes"`
}
