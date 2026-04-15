// Typed wrappers around RPCClient.Call, one per RPC method we need. Each
// wrapper exists purely to give the rest of the program a type-safe API:
// instead of passing around strings and interface{} values, we call
// GetWalletInfo() and get back a WalletInfo struct the compiler understands.
//
// If you want to add a new RPC:
//   1. Add a response struct to rpc_types.go (or reuse an existing one)
//   2. Add a one-line wrapper here that calls c.Call(method, params, &out)
//   3. Use it from update.go in a fetchXyz command
package main

// GetWalletInfo fetches balance / staking flag / unlocked-until state.
// Called on every refresh tick to keep the stats panel up to date.
func (c *RPCClient) GetWalletInfo() (WalletInfo, error) {
	var w WalletInfo
	err := c.Call("getwalletinfo", nil, &w)
	return w, err
}

// GetBlockchainInfo returns the current chain name ("main"/"test") and tip
// height. Used in the header badge and for the network-mismatch warning.
func (c *RPCClient) GetBlockchainInfo() (BlockchainInfo, error) {
	var b BlockchainInfo
	err := c.Call("getblockchaininfo", nil, &b)
	return b, err
}

// GetStakingInfo feeds the staking badge + difficulty column in the stats panel.
func (c *RPCClient) GetStakingInfo() (StakingInfo, error) {
	var s StakingInfo
	err := c.Call("getstakinginfo", nil, &s)
	return s, err
}

// ListSinceBlock is the incremental fetch the TUI actually uses.
//
// It returns wallet transactions that either remain in the
// mempool or were mined after `blockHash`. Pass an empty string on the
// first call to fetch the entire wallet history plus a starting cursor.
//
// `targetConfirms` controls how far back the "lastblock" cursor in the
// response sits: the daemon returns the hash of the block
// `nBestHeight + 1 - targetConfirms`, so we always get a re-confirmation
// window of that many blocks on the next call. We pass 6 so the
// confirmation count of every tx below the "confirmed" threshold stays
// live.
//
// `includeWatchOnly` matches the rest of our code paths — we include
// watch-only addresses everywhere so imported cold-wallet addresses show
// up too.
func (c *RPCClient) ListSinceBlock(blockHash string, targetConfirms int, includeWatchOnly bool) (SinceBlockResponse, error) {
	var out SinceBlockResponse
	// Note: blockHash can be the empty string on the very first call —
	// the daemon's SetHex("") produces a zero hash that is not in the
	// block index, which triggers the "return all transactions" branch.
	err := c.Call("listsinceblock", []any{blockHash, targetConfirms, includeWatchOnly}, &out)
	return out, err
}

// ValidateAddress is the pre-flight check in the send flow. Cheaper than
// attempting a sendtoaddress against a malformed recipient.
func (c *RPCClient) ValidateAddress(addr string) (ValidateAddress, error) {
	var v ValidateAddress
	err := c.Call("validateaddress", []any{addr}, &v)
	return v, err
}

// WalletPassphrase unlocks an encrypted wallet for `timeoutSec` seconds.
// We only ever use this for the duration of a single sendtoaddress call,
// then immediately follow up with WalletLock.
func (c *RPCClient) WalletPassphrase(passphrase string, timeoutSec int) error {
	return c.Call("walletpassphrase", []any{passphrase, timeoutSec}, nil)
}

// WalletLock re-locks the wallet after a send, so the passphrase isn't
// sitting around in memory longer than the one operation needs it.
func (c *RPCClient) WalletLock() error {
	return c.Call("walletlock", nil, nil)
}

// SendToAddress broadcasts a GRC transfer and returns the resulting txid.
func (c *RPCClient) SendToAddress(addr string, amount float64) (string, error) {
	var txid string
	err := c.Call("sendtoaddress", []any{addr, amount}, &txid)
	return txid, err
}

// ListReceivedByAddress returns every address the wallet knows about.
// Parameters: minconf=0 (include 0-conf receives), include_empty=true
// (show addresses with no received amount too), include_watchonly=true
// (include imported watch-only addresses). Populates the "My Addresses" panel.
func (c *RPCClient) ListReceivedByAddress() ([]ReceivedAddress, error) {
	var out []ReceivedAddress
	err := c.Call("listreceivedbyaddress", []any{0, true, true}, &out)
	return out, err
}
