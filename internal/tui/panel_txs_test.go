// Tests for the contract-transaction feature (issue #7): decoding the
// gettransaction "contracts" array, spotting a contract burn in a
// listsinceblock entry, the fetch-once cache bookkeeping, and the two places
// the contract type surfaces in the UI (the tx row's address column and the
// tx detail modal). See rpc_test.go for a testing primer.
package tui

import (
	"strings"
	"testing"

	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
)

// TestUncachedContractTxIDs guards the fetch-once policy. Every entry it
// returns costs one gettransaction RPC, so it must skip non-candidates
// entirely and, the subtle half, treat a cached empty string as an answer
// ("we asked, there is no contract") rather than a miss. Getting that wrong
// re-fetches the same txids on every wallet refresh, forever.
func TestUncachedContractTxIDs(t *testing.T) {
	m := Model{
		txs: []rpc.Transaction{
			{Category: "send", Address: "", TxID: "cached-beacon"},
			{Category: "send", Address: "", TxID: "unresolved"},
			{Category: "send", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ", TxID: "payment"},
			{Category: "generate", Address: "", TxID: "stake"},
		},
		txContracts: map[string]string{
			"cached-beacon": "beacon",
			// A negative cache entry for a txid that isn't even in the list:
			// it must not leak into the result.
			"gone-from-list": "",
		},
	}
	got := m.uncachedContractTxIDs()
	if len(got) != 1 || got[0] != "unresolved" {
		t.Errorf("uncachedContractTxIDs() = %v, want [unresolved]", got)
	}

	// Once every candidate has an answer, including the negative one, there
	// is nothing left to fetch.
	m.txContracts["unresolved"] = ""
	if got := m.uncachedContractTxIDs(); len(got) != 0 {
		t.Errorf("all-cached model returned %v, want nothing to fetch", got)
	}
}

// TestTxsMsgContractFetchAccounting guards the inflight counter, which is what
// the footer spinner is driven by: every command dispatched here must be
// matched by exactly one bumpInflight, and every result handler by exactly one
// finishFetch. Miscount it and the spinner either never starts or spins
// forever, and neither shows up as a test failure anywhere else. The txsMsg
// handler is the only place that can dispatch TWO follow-up fetches at once,
// so it is where the arithmetic is easiest to get wrong.
func TestTxsMsgContractFetchAccounting(t *testing.T) {
	beacon := rpc.Transaction{Category: "send", Address: "", TxID: "beacon", Amount: -0.5, Time: 100}
	payment := rpc.Transaction{Category: "send", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ", TxID: "pay", Amount: -1, Time: 90}
	vote := rpc.Transaction{Category: "send", Address: "", TxID: "vote", Amount: -0.01, Time: 110}

	// Initial load: txsLoaded is false, so no address refresh is chained, but
	// the whole history arrives at once and its contracts need resolving:
	// one fetch, one increment. Net: -1 for this message, +1 for the fetch.
	m := Model{inflight: 1, txContracts: map[string]string{}}
	next, cmd := m.Update(txsMsg{resp: rpc.SinceBlockResponse{Transactions: []rpc.Transaction{beacon, payment}}})
	got := next.(Model)
	if cmd == nil {
		t.Fatal("initial load with an unresolved contract should dispatch a fetch")
	}
	if got.inflight != 1 {
		t.Errorf("inflight = %d, want 1 (one fetch dispatched)", got.inflight)
	}

	// The fetch lands. One message, one finishFetch, back to idle, and the
	// empty value is stored as a real answer.
	next, _ = got.Update(txContractsMsg{types: map[string]string{"beacon": "beacon"}})
	got = next.(Model)
	if got.inflight != 0 {
		t.Errorf("inflight = %d, want 0 once the fetch lands", got.inflight)
	}
	if got.txContracts["beacon"] != "beacon" {
		t.Errorf("txContracts = %v, want beacon resolved", got.txContracts)
	}

	// An idle tick re-delivering the same transactions: nothing new, nothing
	// unresolved, so nothing may be dispatched. This is the case that runs
	// every few seconds forever, so a stray fetch here is a permanent RPC leak.
	got.inflight = 1
	next, cmd = got.Update(txsMsg{resp: rpc.SinceBlockResponse{Transactions: []rpc.Transaction{beacon, payment}}})
	got = next.(Model)
	if cmd != nil {
		t.Error("an idle tick with everything cached must dispatch nothing")
	}
	if got.inflight != 0 {
		t.Errorf("inflight = %d, want 0 on an idle tick", got.inflight)
	}

	// A brand-new contract transaction arrives after the initial load. That is
	// the only path dispatching two fetches from one message (addresses AND
	// contracts), so the counter must move by two.
	got.inflight = 1
	next, cmd = got.Update(txsMsg{resp: rpc.SinceBlockResponse{Transactions: []rpc.Transaction{vote}}})
	got = next.(Model)
	if cmd == nil {
		t.Fatal("a new contract tx should dispatch fetches")
	}
	if got.inflight != 2 {
		t.Errorf("inflight = %d, want 2 (addresses + contracts)", got.inflight)
	}
}

// TestRenderTxRowContract covers the address column, which is the whole
// user-visible point of the feature: a contract burn has no counterparty, so
// the column would otherwise be blank and the row would read as broken data.
// Styles are live from theme.go's init(), so no setup is needed; the output
// carries ANSI escapes, hence the substring assertions.
func TestRenderTxRowContract(t *testing.T) {
	contract := rpc.Transaction{Category: "send", Address: "", TxID: "c1", Amount: -0.01, Confirmations: 10}

	out := renderTxRow(contract, false, "vote")
	if !strings.Contains(out, "(vote)") {
		t.Errorf("a resolved contract should name its type, got:\n%s", out)
	}

	// Empty cache value: honest generic label while the lookup is in flight
	// (or when the daemon itself couldn't classify the contract).
	out = renderTxRow(contract, false, "")
	if !strings.Contains(out, "(contract)") {
		t.Errorf("an unresolved contract should fall back to (contract), got:\n%s", out)
	}

	// A stake is also address-less but is NOT a contract, so it keeps its own
	// label.
	stake := rpc.Transaction{Category: "generate", Address: "", TxID: "s1", Amount: 2.5, Confirmations: 30}
	out = renderTxRow(stake, false, "")
	if !strings.Contains(out, "(stake)") {
		t.Errorf("stake row should read (stake), got:\n%s", out)
	}
	if strings.Contains(out, "(contract)") {
		t.Errorf("stake row must not be labelled a contract, got:\n%s", out)
	}

	// The type is a string from the daemon, not a value we control, so the
	// column has to survive one longer than the types we know about. The
	// label is built first and shortened after, so ShortAddress caps it at
	// its usual 10 characters and the fixed-width layout holds.
	out = renderTxRow(contract, false, "averylongcontracttypename")
	if strings.Contains(out, "averylongcontracttypename") {
		t.Errorf("an over-long type must be elided, not rendered whole, got:\n%s", out)
	}

	// An ordinary payment still shows its shortened counterparty address.
	payment := rpc.Transaction{Category: "send", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ", TxID: "p1", Amount: -12.5, Confirmations: 10}
	out = renderTxRow(payment, false, "")
	if !strings.Contains(out, "SGrcPa…QpZ") {
		t.Errorf("payment row should show the shortened address, got:\n%s", out)
	}
	if strings.Contains(out, "(contract)") {
		t.Errorf("payment row must not be labelled a contract, got:\n%s", out)
	}
}
