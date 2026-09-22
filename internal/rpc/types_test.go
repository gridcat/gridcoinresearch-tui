package rpc

import (
	"encoding/json"
	"testing"
)

func TestPeerInfoDecodesAddrAndInbound(t *testing.T) {
	// The fields the TUI actually reads, straight off a getpeerinfo shape.
	const payload = `[{"id":1,"addr":"45.33.32.156:32749","inbound":false,"subver":"/Gridcoin:5.5.0.0/"}]`
	var peers []PeerInfo
	if err := json.Unmarshal([]byte(payload), &peers); err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].Addr != "45.33.32.156:32749" || peers[0].Inbound {
		t.Errorf("decoded %+v", peers[0])
	}
}

// TestPollJSONDecode confirms a real-shaped listpolls entry decodes, including
// the choices array and the vote count.
func TestPollJSONDecode(t *testing.T) {
	const body = `{
		"title": "Fund outreach 2026",
		"id": "abc123",
		"question": "Should we?",
		"url": "https://example.org",
		"weight_type": "Magnitude",
		"response_type": "Yes/No/Abstain",
		"duration_days": 7,
		"expiration": "03-09-2026 14:05:06",
		"timestamp": "03-02-2026 14:05:06",
		"choices": [{"id": 0, "label": "Yes"}, {"id": 1, "label": "No"}],
		"votes": 14
	}`
	var p Poll
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Title != "Fund outreach 2026" || p.WeightType != "Magnitude" || p.Votes != 14 {
		t.Errorf("scalar fields wrong: %+v", p)
	}
	if p.Question != "Should we?" || p.URL != "https://example.org" ||
		p.ResponseType != "Yes/No/Abstain" || p.DurationDays != 7 || p.Timestamp != "03-02-2026 14:05:06" {
		t.Errorf("detail fields wrong: %+v", p)
	}
}

// TestPollResultJSONDecode covers the two tricky shapes getpollresults emits:
// vote_percent_avw present as a number (decoded through the pointer), and
// top_choice arriving as JSON null (decoded to the empty string).
func TestPollResultJSONDecode(t *testing.T) {
	// responses[].votes can be fractional (a split vote in a multiple-choice
	// poll), so the daemon sends e.g. 153.5, and it must decode into a float field.
	withVotes := `{"poll_id":"x","poll_expired":false,"votes":3,"total_weight":12.5,"vote_percent_avw":62.4,"top_choice":"Yes","responses":[{"choice":"Yes","id":0,"weight":10,"votes":153.5}]}`
	var r PollResult
	if err := json.Unmarshal([]byte(withVotes), &r); err != nil {
		t.Fatalf("decode withVotes: %v", err)
	}
	if r.VotePercentAVW == nil || *r.VotePercentAVW != 62.4 {
		t.Errorf("vote_percent_avw = %v, want 62.4", r.VotePercentAVW)
	}
	if r.TopChoice != "Yes" || r.TotalWeight != 12.5 {
		t.Errorf("top_choice/total_weight wrong: %+v", r)
	}
	if len(r.Responses) != 1 || r.Responses[0].Choice != "Yes" || r.Responses[0].Weight != 10 || r.Responses[0].Votes != 153.5 {
		t.Errorf("responses wrong: %+v", r.Responses)
	}

	noVotes := `{"poll_id":"y","poll_expired":true,"votes":0,"total_weight":0,"top_choice":null,"responses":[]}`
	var r2 PollResult
	if err := json.Unmarshal([]byte(noVotes), &r2); err != nil {
		t.Fatalf("decode noVotes: %v", err)
	}
	if r2.VotePercentAVW != nil {
		t.Errorf("vote_percent_avw should be nil when absent, got %v", *r2.VotePercentAVW)
	}
	if r2.TopChoice != "" {
		t.Errorf("top_choice null should decode to empty string, got %q", r2.TopChoice)
	}
}

// TestTxDetailDecode pins the gettransaction decode against the shapes the
// daemon really emits. TxDetail picks exactly one field (contracts[].type)
// out of a large response, and the fixtures below reproduce the response's
// awkward parts (a repeated key, and a "body" that is an object in one
// contract type and a bare string in another) so that a future attempt to
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

	// A vote, the other type the issue explicitly asked for. Its body is an
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
	// passing and only this one fails, which is exactly the bug, because the
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
	// empty slice rather than erroring. This is the common case, since the
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

// TestStakingInfoResearcherDecode confirms both shapes of getstakinginfo:
// a cruncher's response carries current_magnitude and BoincRewardPending,
// an investor's response omits both fields entirely (the daemon only pushes
// them when a CPID is configured), which must decode to nil pointers.
func TestStakingInfoResearcherDecode(t *testing.T) {
	const cruncher = `{
		"blocks": 1234567,
		"staking": true,
		"mining-error": "",
		"expectedtime": 86400,
		"CPID": "8edc235ddceca9d6ad76d8e8fc9fe27e",
		"current_magnitude": 245.75,
		"Magnitude Unit": 0.25,
		"BoincRewardPending": 12.3456789
	}`
	var s StakingInfo
	if err := json.Unmarshal([]byte(cruncher), &s); err != nil {
		t.Fatalf("decode cruncher: %v", err)
	}
	if s.Magnitude == nil || *s.Magnitude != 245.75 {
		t.Errorf("Magnitude = %v, want 245.75", s.Magnitude)
	}
	if s.PendingReward == nil || *s.PendingReward != 12.3456789 {
		t.Errorf("PendingReward = %v, want 12.3456789", s.PendingReward)
	}
	if !s.IsCruncher() {
		t.Errorf("IsCruncher() = false for CPID %q, want true", s.CPID)
	}

	// The CPID field is always present, unlike the two above. When there is
	// no CPID the daemon substitutes a placeholder word, and WHICH word has
	// moved between versions: 5.5.0's MiningId::ToString() returns
	// "NONCRUNCHER", older reports say "INVESTOR", and an unset id gives "".
	// That churn is exactly why IsCruncher tests the length of a real CPID
	// instead of matching the placeholders, so pin all three here.
	const investor = `{
		"blocks": 1234567,
		"staking": true,
		"mining-error": "",
		"expectedtime": 86400,
		"CPID": "NONCRUNCHER"
	}`
	s = StakingInfo{}
	if err := json.Unmarshal([]byte(investor), &s); err != nil {
		t.Fatalf("decode investor: %v", err)
	}
	if s.Magnitude != nil || s.PendingReward != nil {
		t.Errorf("investor should decode to nil pointers, got mag=%v pending=%v",
			s.Magnitude, s.PendingReward)
	}
	for _, cpid := range []string{"NONCRUNCHER", "INVESTOR", ""} {
		if (StakingInfo{CPID: cpid}).IsCruncher() {
			t.Errorf("IsCruncher() = true for placeholder %q, want false", cpid)
		}
	}
}

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
