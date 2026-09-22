// Unit tests. A quick Go testing primer for the unfamiliar:
//
//   - Test functions must start with "Test" and take *testing.T.
//   - `go test ./...` discovers and runs every Test* function.
//   - t.Errorf records a failure and keeps running; t.Fatal stops this test.
//   - t.Run(name, func) creates a "sub-test" so table-driven tests get
//     individual pass/fail lines in the output.
//   - t.Setenv sets an env var for the duration of the test and restores
//     it afterwards, which is safer than poking os.Setenv directly.
//   - t.TempDir() gives a unique scratch directory that is auto-deleted
//     when the test ends.
//   - httptest.NewServer spins up a real HTTP server on a random port that
//     the test can point its client at, so we don't need a real daemon.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gridcat/gridcoinresearch-tui/internal/config"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// segText concatenates the visible text of a row's segments so a test can
// assert what the My Addresses panel shows without caring about styling.
func segText(segs []ui.Seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

// TestAddressRowOwnership covers the fix for the "My Addresses shows
// not-is-mine addresses" issue: listreceivedbyaddress returns the whole
// address book, so a foreign (validateaddress ismine=false) address must be
// flagged, while owned and not-yet-resolved addresses must NOT be flagged.
func TestAddressRowOwnership(t *testing.T) {
	addr := rpc.ReceivedAddress{Address: "S1foreign", Account: "alice"}
	cases := []struct {
		name     string
		own      addrOwnership
		wantWarn bool
	}{
		{"foreign address is flagged", ownForeign, true},
		{"own address is not flagged", ownMine, false},
		{"unresolved address is not flagged", ownUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := segText(addressRowSegments(addr, false, tc.own))
			if has := strings.Contains(got, "not yours"); has != tc.wantWarn {
				t.Errorf("row %q: warn=%v, want %v", got, has, tc.wantWarn)
			}
		})
	}
}

// TestOwnershipHelpers checks the cache lookups that drive the panel: a missing
// key is unknown, and unknownOwnership returns exactly the unresolved rows.
func TestOwnershipHelpers(t *testing.T) {
	m := Model{
		addresses: []rpc.ReceivedAddress{{Address: "mine"}, {Address: "foreign"}, {Address: "new"}},
		addrMine:  map[string]bool{"mine": true, "foreign": false},
	}
	if got := m.ownership("mine"); got != ownMine {
		t.Errorf(`ownership("mine") = %v, want ownMine`, got)
	}
	if got := m.ownership("foreign"); got != ownForeign {
		t.Errorf(`ownership("foreign") = %v, want ownForeign`, got)
	}
	if got := m.ownership("new"); got != ownUnknown {
		t.Errorf(`ownership("new") = %v, want ownUnknown`, got)
	}
	unknown := m.unknownOwnership()
	if len(unknown) != 1 || unknown[0] != "new" {
		t.Errorf("unknownOwnership() = %v, want [new]", unknown)
	}
}

// addr is a tiny helper to keep the fixture readable.
func addr(a string) rpc.ReceivedAddress { return rpc.ReceivedAddress{Address: a} }

// TestVisibleAddressesPartition checks the Mine/Others/All filter: Mine keeps
// everything that isn't known-foreign (own + unknown), Others keeps exactly the
// foreign ones, All returns the full slice, and the two halves reconstruct All
// with no gaps or overlap.
func TestVisibleAddressesPartition(t *testing.T) {
	m := Model{
		addresses: []rpc.ReceivedAddress{addr("mine1"), addr("foreign1"), addr("unknown1"), addr("mine2")},
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
		addresses: []rpc.ReceivedAddress{addr("mine1"), addr("foreign1"), addr("unknown1"), addr("mine2")},
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

func TestVisibleAddressesSearchesLabelsAndAddresses(t *testing.T) {
	m := NewModel(config.Config{}, nil)
	m.addrTab = addrTabAll
	m.addresses = []rpc.ReceivedAddress{
		{Address: "one", Account: "Monthly Savings"},
		{Address: "two", Label: "Coffee money"},
		{Address: "savings-address", Account: "Unrelated"},
		{Address: "three"},
	}
	m.addrSearch.SetValue("  SAVings ")

	got := m.visibleAddresses()
	if len(got) != 1 || got[0].Address != "one" {
		t.Fatalf("filtered addresses = %+v, want only the matching label", got)
	}

	m.addrSearch.SetValue("savings-address")
	got = m.visibleAddresses()
	if len(got) != 1 || got[0].Address != "savings-address" {
		t.Fatalf("address search = %+v, want only the matching address", got)
	}
}

func TestAddressSearchKeyboardFlow(t *testing.T) {
	m := NewModel(config.Config{}, nil)
	m.mode = modeDashboard
	m.focusedArea = focusTx
	m.addresses = []rpc.ReceivedAddress{{Account: "Alpha"}, {Account: "Beta"}}

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)
	if !m.addrSearch.Focused() || m.focusedArea != focusAddr {
		t.Fatalf("slash should focus address search, focused=%v area=%v", m.addrSearch.Focused(), m.focusedArea)
	}

	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("beta")})
	m = next.(Model)
	if got := m.visibleAddresses(); len(got) != 1 || got[0].DisplayLabel() != "Beta" {
		t.Fatalf("live filter = %+v, want Beta", got)
	}

	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.addrSearch.Focused() || m.addrSearch.Value() != "beta" {
		t.Fatalf("enter should retain a blurred filter, focused=%v value=%q", m.addrSearch.Focused(), m.addrSearch.Value())
	}

	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.addrSearch.Value() != "" || len(m.visibleAddresses()) != 2 {
		t.Fatalf("esc should clear filter, value=%q visible=%d", m.addrSearch.Value(), len(m.visibleAddresses()))
	}
}
