// Tests for the peer-count feature: getpeerinfo JSON decoding, the peersMsg
// handler's in/out tallying, and the header rendering of the peers fragment.
// See rpc_test.go for a testing primer.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPeerInfoJSONDecode confirms a real-shaped getpeerinfo entry decodes:
// we only keep the inbound flag and ignore the rest of the (large) object.
func TestPeerInfoJSONDecode(t *testing.T) {
	const body = `[
		{"addr": "1.2.3.4:32749", "services": "00000005", "inbound": true, "banscore": 0},
		{"addr": "5.6.7.8:32749", "services": "00000005", "inbound": false, "banscore": 0},
		{"addr": "9.9.9.9:32749", "services": "00000005", "inbound": false, "banscore": 0}
	]`
	var peers []PeerInfo
	if err := json.Unmarshal([]byte(body), &peers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(peers) != 3 {
		t.Fatalf("got %d peers, want 3", len(peers))
	}
	if !peers[0].Inbound || peers[1].Inbound || peers[2].Inbound {
		t.Errorf("inbound flags wrong: %+v", peers)
	}
}

// TestPeersMsgHandler feeds a peersMsg through Update and checks the model
// tallies total/in/out and flips peersLoaded; an error must leave the
// previous counts untouched.
func TestPeersMsgHandler(t *testing.T) {
	m := Model{inflight: 1}
	next, _ := m.Update(peersMsg{peers: []PeerInfo{
		{Inbound: true}, {Inbound: false}, {Inbound: false}, {Inbound: true}, {Inbound: false},
	}})
	got := next.(Model)
	if got.peersTotal != 5 || got.peersIn != 2 || got.peersOut != 3 {
		t.Errorf("counts = %d (%d in / %d out), want 5 (2 in / 3 out)",
			got.peersTotal, got.peersIn, got.peersOut)
	}
	if !got.peersLoaded {
		t.Error("peersLoaded should be true after a successful fetch")
	}
	if got.inflight != 0 {
		t.Errorf("inflight = %d, want 0 (finishFetch must run)", got.inflight)
	}

	// A failed fetch keeps the last-known counts on screen, still counts
	// as a finished fetch (inflight decrements), and surfaces the error.
	got.inflight = 1
	next2, _ := got.Update(peersMsg{err: errFake})
	got2 := next2.(Model)
	if got2.peersTotal != 5 || got2.peersIn != 2 || got2.peersOut != 3 || !got2.peersLoaded {
		t.Errorf("error must not clobber counts: got %d (%d in / %d out), loaded=%v",
			got2.peersTotal, got2.peersIn, got2.peersOut, got2.peersLoaded)
	}
	if got2.inflight != 0 {
		t.Errorf("inflight = %d, want 0 (finishFetch must run on the error path too)", got2.inflight)
	}
	if got2.walletErr != errFake.Error() {
		t.Errorf("walletErr = %q, want %q", got2.walletErr, errFake.Error())
	}
}

// errFake is a trivial error value for handler tests.
var errFake = fakeErr{}

type fakeErr struct{}

func (fakeErr) Error() string { return "fake rpc failure" }

// TestRenderHeaderPeers checks the three header states: loaded with peers
// (block + peers joined with a separator), loaded with zero peers (warning,
// no in/out split), and not yet loaded (no peers fragment at all).
func TestRenderHeaderPeers(t *testing.T) {
	m := Model{
		width:       100,
		chain:       BlockchainInfo{Chain: "main", Blocks: 1234567},
		peersLoaded: true,
		peersTotal:  8,
		peersIn:     3,
		peersOut:    5,
	}
	out := m.renderHeader()
	if !strings.Contains(out, "peers 8 (3↓/5↑)") {
		t.Errorf("header missing peers fragment, got:\n%s", out)
	}
	if !strings.Contains(out, "block 1,234,567") {
		t.Errorf("header missing block info, got:\n%s", out)
	}

	m.peersTotal, m.peersIn, m.peersOut = 0, 0, 0
	out = m.renderHeader()
	if !strings.Contains(out, "peers 0") {
		t.Errorf("zero peers should render a warning fragment, got:\n%s", out)
	}
	if strings.Contains(out, "(0↓/0↑)") {
		t.Errorf("zero peers should not render an in/out split, got:\n%s", out)
	}

	m.peersLoaded = false
	out = m.renderHeader()
	if strings.Contains(out, "peers") {
		t.Errorf("unloaded peers should render nothing, got:\n%s", out)
	}
}
