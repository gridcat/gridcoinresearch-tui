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

// BlockchainInfo matches getblockchaininfo. We only use Chain and Blocks in
// the UI today; the rest is kept for future use.
type BlockchainInfo struct {
	Chain                string  `json:"chain"` // "main" or "test"
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
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
type ReceivedAddress struct {
	Address       string  `json:"address"`
	Amount        float64 `json:"amount"`
	Confirmations int64   `json:"confirmations"`
	Label         string  `json:"label"`   // newer bitcoin-core style
	Account       string  `json:"account"` // legacy field still emitted by gridcoinresearchd
}

// DisplayLabel returns the label to show next to the address in the UI,
// preferring the newer field if both happen to be present.
func (r ReceivedAddress) DisplayLabel() string {
	if r.Label != "" {
		return r.Label
	}
	return r.Account
}
