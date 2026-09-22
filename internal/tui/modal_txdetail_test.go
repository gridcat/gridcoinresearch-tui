package tui

import (
	"strings"
	"testing"

	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
)

// TestRenderTxDetailContract covers the modal's three-state Contract field.
// The states exist because the lookup is asynchronous and can also come back
// negative, and each has a different correct rendering: the type, a muted
// "resolving…", or no line at all. Collapsing "resolved as not-a-contract"
// into "resolving…" would leave a permanent fake spinner on screen.
func TestRenderTxDetailContract(t *testing.T) {
	contract := rpc.Transaction{
		Category: "send", Address: "", TxID: "c1",
		Amount: -0.01, Confirmations: 128, Time: 1754900000,
	}
	newModel := func(cache map[string]string) Model {
		return Model{width: 100, height: 30, txs: []rpc.Transaction{contract}, txCursor: 0, txContracts: cache}
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

	// Cached as "" = asked, no contract. The field disappears, and so does
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
	receive := rpc.Transaction{
		Category: "receive", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ", TxID: "r1",
		Amount: 12.5, Confirmations: 42, Time: 1754930000,
	}
	m := Model{width: 100, height: 30, txs: []rpc.Transaction{receive}, txCursor: 0, txContracts: map[string]string{}}
	out = m.renderTxDetailModal()
	if strings.Contains(out, "Contract") || strings.Contains(out, "no destination address") {
		t.Errorf("a plain receive should have no contract rendering at all, got:\n%s", out)
	}
}

// TestRenderTxDetailAddressLabel ensures the transaction popup joins a normal
// payment to the cached wallet address book. listsinceblock does not return
// labels itself, so this is deliberately a lookup rather than a new RPC call.
func TestRenderTxDetailAddressLabel(t *testing.T) {
	const address = "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ"
	tx := rpc.Transaction{Category: "send", Address: address, TxID: "labelled", Amount: -1}
	newModel := func(label string) Model {
		return Model{
			width: 100, height: 30, txs: []rpc.Transaction{tx}, txCursor: 0,
			addresses: []rpc.ReceivedAddress{{Address: address, Account: label}},
		}
	}

	out := newModel("household wallet").renderTxDetailModal()
	if !strings.Contains(out, "Label") || !strings.Contains(out, "household wallet") {
		t.Errorf("transaction detail should show the cached address label, got:\n%s", out)
	}

	out = newModel("").renderTxDetailModal()
	if strings.Contains(out, "Label") {
		t.Errorf("transaction detail should omit an empty label, got:\n%s", out)
	}

	out = newModel("saved\x1b[31m recipient").renderTxDetailModal()
	if strings.Contains(out, "\x1b[31m") || !strings.Contains(out, "saved[31m recipient") {
		t.Errorf("transaction detail should sanitize the cached label, got:\n%s", out)
	}
}
