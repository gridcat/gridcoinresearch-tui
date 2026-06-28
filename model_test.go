// Tests for the My Addresses tab filter. See rpc_test.go for a testing primer.
package main

import "testing"

// addr is a tiny helper to keep the fixture readable.
func addr(a string) ReceivedAddress { return ReceivedAddress{Address: a} }

// TestVisibleAddressesPartition checks the Mine/Others/All filter: Mine keeps
// everything that isn't known-foreign (own + unknown), Others keeps exactly the
// foreign ones, All returns the full slice — and the two halves reconstruct All
// with no gaps or overlap.
func TestVisibleAddressesPartition(t *testing.T) {
	m := Model{
		addresses: []ReceivedAddress{addr("mine1"), addr("foreign1"), addr("unknown1"), addr("mine2")},
		// "unknown1" is intentionally absent so ownership() reports ownUnknown.
		addrMine: map[string]bool{
			"mine1":    true,
			"mine2":    true,
			"foreign1": false,
		},
	}

	cases := []struct {
		tab  addrTab
		want []string
	}{
		{addrTabMine, []string{"mine1", "unknown1", "mine2"}}, // own + unknown
		{addrTabOthers, []string{"foreign1"}},                 // foreign only
		{addrTabAll, []string{"mine1", "foreign1", "unknown1", "mine2"}},
	}
	for _, c := range cases {
		m.addrTab = c.tab
		got := m.visibleAddresses()
		if len(got) != len(c.want) {
			t.Errorf("tab %d: got %d rows, want %d", c.tab, len(got), len(c.want))
			continue
		}
		for i, a := range got {
			if a.Address != c.want[i] {
				t.Errorf("tab %d: row %d = %q, want %q", c.tab, i, a.Address, c.want[i])
			}
		}
	}

	// Mine + Others must reconstruct All exactly.
	m.addrTab = addrTabMine
	mineN := len(m.visibleAddresses())
	m.addrTab = addrTabOthers
	othersN := len(m.visibleAddresses())
	if mineN+othersN != len(m.addresses) {
		t.Errorf("Mine(%d) + Others(%d) != All(%d)", mineN, othersN, len(m.addresses))
	}
}

// TestAddrTabCounts checks the tab-bar counts: others counts known-foreign,
// mine is the rest (own + unknown), all is the total.
func TestAddrTabCounts(t *testing.T) {
	m := Model{
		addresses: []ReceivedAddress{addr("mine1"), addr("foreign1"), addr("unknown1"), addr("mine2")},
		addrMine: map[string]bool{
			"mine1":    true,
			"mine2":    true,
			"foreign1": false,
		},
	}
	mine, others, all := m.addrTabCounts()
	if mine != 3 || others != 1 || all != 4 {
		t.Errorf("counts = (mine %d, others %d, all %d), want (3, 1, 4)", mine, others, all)
	}
}
