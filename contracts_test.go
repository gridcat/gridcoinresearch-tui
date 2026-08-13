// Tests for the contract-transaction feature (issue #7): decoding the
// gettransaction "contracts" array, spotting a contract burn in a
// listsinceblock entry, the fetch-once cache bookkeeping, and the two places
// the contract type surfaces in the UI (the tx row's address column and the
// tx detail modal). See rpc_test.go for a testing primer.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTxDetailDecode pins the gettransaction decode against the shapes the
// daemon really emits. TxDetail picks exactly one field (contracts[].type)
// out of a large response, and the fixtures below reproduce the response's
// awkward parts — a repeated key, and a "body" that is an object in one
// contract type and a bare string in another — so that a future attempt to
// decode more of it fails here rather than in the field.
func TestTxDetailDecode(t *testing.T) {
	// A beacon advertisement. txid/time appear TWICE, exactly as the daemon
	// sends them: gettransaction builds its object from two serialisers and
	// pushes both sets. Duplicate keys are legal JSON and encoding/json keeps
	// the last, so this must decode without complaint.
	const beacon = `{
		"amount": -0.01,
		"fee": -0.0001,
		"confirmations": 128,
		"blockhash": "5f0c9d2b6ae1e0f7c1a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f607",
		"blockindex": 2,
		"blocktime": 1754900000,
		"txid": "9c4f1e2d3a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5",
		"time": 1754900000,
		"txid": "9c4f1e2d3a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5",
		"time": 1754900000,
		"timereceived": 1754900123,
		"details": [{"account": "", "category": "send", "amount": -0.01, "fee": -0.0001}],
		"contracts": [
			{
				"version": 3,
				"type": "beacon",
				"action": "A",
				"body": {
					"cpid": "8edc235ddceca9d6ad76d8e8fc9fe27e",
					"address": "S6UbxJ7Y3kqRcTBGtRRnHTsVvNSyPKcAX2",
					"public_key": "04a1b2c3d4e5f60718293a4b5c6d7e8f90",
					"timestamp": 1754900000
				}
			}
		]
	}`
	var d TxDetail
	if err := json.Unmarshal([]byte(beacon), &d); err != nil {
		t.Fatalf("decode beacon: %v", err)
	}
	if len(d.Contracts) != 1 || d.Contracts[0].Type != "beacon" {
		t.Errorf("beacon contracts = %+v, want one entry of type beacon", d.Contracts)
	}

	// A vote — the other type the issue explicitly asked for. Its body is an
	// object of a completely different shape from the beacon's, which is the
	// reason body isn't decoded at all.
	const vote = `{
		"amount": -0.01,
		"fee": -0.0001,
		"confirmations": 7,
		"txid": "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809",
		"time": 1754910000,
		"contracts": [
			{
				"version": 3,
				"type": "vote",
				"action": "A",
				"body": {
					"poll_txid": "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
					"responses": [1]
				}
			}
		]
	}`
	d = TxDetail{}
	if err := json.Unmarshal([]byte(vote), &d); err != nil {
		t.Fatalf("decode vote: %v", err)
	}
	if len(d.Contracts) != 1 || d.Contracts[0].Type != "vote" {
		t.Errorf("vote contracts = %+v, want one entry of type vote", d.Contracts)
	}

	// A "message" contract's body is a bare JSON STRING, not an object. This
	// is the single most valuable fixture in this file: the day someone
	// decides to decode body into a struct, every other fixture here keeps
	// passing and only this one fails — which is exactly the bug, because the
	// daemon really does send both shapes under the same key.
	const message = `{
		"amount": -0.01,
		"confirmations": 3,
		"txid": "deadbeef00112233445566778899aabbccddeeff00112233445566778899aabb",
		"time": 1754920000,
		"contracts": [
			{"version": 2, "type": "message", "action": "A", "body": "hello from the blockchain"}
		]
	}`
	d = TxDetail{}
	if err := json.Unmarshal([]byte(message), &d); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if len(d.Contracts) != 1 || d.Contracts[0].Type != "message" {
		t.Errorf("message contracts = %+v, want one entry of type message", d.Contracts)
	}

	// An ordinary payment: no "contracts" key at all, which must decode to an
	// empty slice rather than erroring — this is the common case, since the
	// modal's retry path calls gettransaction on anything the batch missed.
	const plain = `{
		"amount": -12.5,
		"fee": -0.0001,
		"confirmations": 42,
		"txid": "0011223344556677889900aabbccddeeff0011223344556677889900aabbccdd",
		"time": 1754930000,
		"details": [{"account": "", "address": "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ", "category": "send", "amount": -12.5}]
	}`
	d = TxDetail{}
	if err := json.Unmarshal([]byte(plain), &d); err != nil {
		t.Fatalf("decode plain: %v", err)
	}
	if len(d.Contracts) != 0 {
		t.Errorf("plain send contracts = %+v, want none", d.Contracts)
	}
}

// TestIsContractCandidate pins the shape-sniffing rule that stands in for the
// category listsinceblock never provides: a contract burn is a "send" with an
// empty address. Both halves matter — dropping the category check would swallow
// stakes (also address-less), dropping the address check would call every
// payment a contract.
func TestIsContractCandidate(t *testing.T) {
	cases := []struct {
		name string
		tx   Transaction
		want bool
	}{
		{"send with no address is a contract burn", Transaction{Category: "send", Address: ""}, true},
		{"send to a real address is a payment", Transaction{Category: "send", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ"}, false},
		{"stake has no address either", Transaction{Category: "generate", Address: ""}, false},
		{"immature stake has no address either", Transaction{Category: "immature", Address: ""}, false},
		{"receive is never a contract", Transaction{Category: "receive", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsContractCandidate(tc.tx); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUncachedContractTxIDs guards the fetch-once policy. Every entry it
// returns costs one gettransaction RPC, so it must skip non-candidates
// entirely and — the subtle half — treat a cached empty string as an answer
// ("we asked, there is no contract") rather than a miss. Getting that wrong
// re-fetches the same txids on every wallet refresh, forever.
func TestUncachedContractTxIDs(t *testing.T) {
	m := Model{
		txs: []Transaction{
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

	// Once every candidate has an answer — including the negative one — there
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
	beacon := Transaction{Category: "send", Address: "", TxID: "beacon", Amount: -0.5, Time: 100}
	payment := Transaction{Category: "send", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ", TxID: "pay", Amount: -1, Time: 90}
	vote := Transaction{Category: "send", Address: "", TxID: "vote", Amount: -0.01, Time: 110}

	// Initial load: txsLoaded is false, so no address refresh is chained, but
	// the whole history arrives at once and its contracts need resolving —
	// one fetch, one increment. Net: -1 for this message, +1 for the fetch.
	m := Model{inflight: 1, txContracts: map[string]string{}}
	next, cmd := m.Update(txsMsg{resp: SinceBlockResponse{Transactions: []Transaction{beacon, payment}}})
	got := next.(Model)
	if cmd == nil {
		t.Fatal("initial load with an unresolved contract should dispatch a fetch")
	}
	if got.inflight != 1 {
		t.Errorf("inflight = %d, want 1 (one fetch dispatched)", got.inflight)
	}

	// The fetch lands. One message, one finishFetch, back to idle — and the
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
	next, cmd = got.Update(txsMsg{resp: SinceBlockResponse{Transactions: []Transaction{beacon, payment}}})
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
	next, cmd = got.Update(txsMsg{resp: SinceBlockResponse{Transactions: []Transaction{vote}}})
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
// Styles are live from view.go's init(), so no setup is needed; the output
// carries ANSI escapes, hence the substring assertions.
func TestRenderTxRowContract(t *testing.T) {
	contract := Transaction{Category: "send", Address: "", TxID: "c1", Amount: -0.01, Confirmations: 10}

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

	// A stake is also address-less but is NOT a contract — it keeps its own
	// label.
	stake := Transaction{Category: "generate", Address: "", TxID: "s1", Amount: 2.5, Confirmations: 30}
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
	payment := Transaction{Category: "send", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ", TxID: "p1", Amount: -12.5, Confirmations: 10}
	out = renderTxRow(payment, false, "")
	if !strings.Contains(out, "SGrcPa…QpZ") {
		t.Errorf("payment row should show the shortened address, got:\n%s", out)
	}
	if strings.Contains(out, "(contract)") {
		t.Errorf("payment row must not be labelled a contract, got:\n%s", out)
	}
}

// TestRenderTxDetailContract covers the modal's three-state Contract field.
// The states exist because the lookup is asynchronous and can also come back
// negative, and each has a different correct rendering: the type, a muted
// "resolving…", or no line at all. Collapsing "resolved as not-a-contract"
// into "resolving…" would leave a permanent fake spinner on screen.
func TestRenderTxDetailContract(t *testing.T) {
	contract := Transaction{
		Category: "send", Address: "", TxID: "c1",
		Amount: -0.01, Confirmations: 128, Time: 1754900000,
	}
	newModel := func(cache map[string]string) Model {
		return Model{width: 100, height: 30, txs: []Transaction{contract}, txCursor: 0, txContracts: cache}
	}

	out := newModel(map[string]string{"c1": "beacon"}).renderTxDetailModal()
	if !strings.Contains(out, "Contract") || !strings.Contains(out, "beacon") {
		t.Errorf("cached contract should show its type, got:\n%s", out)
	}
	if !strings.Contains(out, "no destination address") {
		t.Errorf("address field should explain the missing counterparty, got:\n%s", out)
	}

	// Missing key = not looked up yet.
	out = newModel(map[string]string{}).renderTxDetailModal()
	if !strings.Contains(out, "resolving…") {
		t.Errorf("uncached contract should show the resolving placeholder, got:\n%s", out)
	}

	// Cached as "" = asked, no contract. The field disappears — and so does
	// every claim that this was a contract, which is why the address line
	// says only that the output was burned. The daemon has just told us it
	// found no contract here; repeating the word would contradict it.
	out = newModel(map[string]string{"c1": ""}).renderTxDetailModal()
	if strings.Contains(out, "Contract") {
		t.Errorf("a tx cached as having no contract should drop the field, got:\n%s", out)
	}
	if strings.Contains(out, "contract") {
		t.Errorf("nothing may call it a contract once the daemon says otherwise, got:\n%s", out)
	}
	if strings.Contains(out, "resolving…") {
		t.Errorf("a resolved lookup must not still say resolving, got:\n%s", out)
	}

	// A plain receive never gets the field, cache or no cache.
	receive := Transaction{
		Category: "receive", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ", TxID: "r1",
		Amount: 12.5, Confirmations: 42, Time: 1754930000,
	}
	m := Model{width: 100, height: 30, txs: []Transaction{receive}, txCursor: 0, txContracts: map[string]string{}}
	out = m.renderTxDetailModal()
	if strings.Contains(out, "Contract") || strings.Contains(out, "no destination address") {
		t.Errorf("a plain receive should have no contract rendering at all, got:\n%s", out)
	}
}
