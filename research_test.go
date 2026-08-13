// Tests for the researcher-stats feature (issue #7): decoding the optional
// current_magnitude / BoincRewardPending fields of getstakinginfo, and the
// conditional "Pending / Magnitude" stats row. See rpc_test.go for a testing
// primer.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

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
	// moved between versions — 5.5.0's MiningId::ToString() returns
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

// TestRenderStatsResearcher checks the three states of the researcher row:
// present with formatted values for a cruncher, absent for an investor, and
// pending masked (but magnitude still visible) in anonymous mode.
func TestRenderStatsResearcher(t *testing.T) {
	mag, pending := 245.75, 12.3456789
	m := Model{
		width:  100,
		loaded: true,
		staking: StakingInfo{
			Magnitude:     &mag,
			PendingReward: &pending,
		},
	}
	out := m.renderStats()
	if !strings.Contains(out, "Magnitude") || !strings.Contains(out, "245.75") {
		t.Errorf("stats missing magnitude, got:\n%s", out)
	}
	if !strings.Contains(out, "Pending Reward") || !strings.Contains(out, "12.35 GRC") {
		t.Errorf("stats missing pending reward, got:\n%s", out)
	}

	m.staking = StakingInfo{}
	out = m.renderStats()
	if strings.Contains(out, "Magnitude") || strings.Contains(out, "Pending Reward") {
		t.Errorf("investor should have no researcher row, got:\n%s", out)
	}

	m.staking = StakingInfo{Magnitude: &mag, PendingReward: &pending}
	m.anonymous = true
	out = m.renderStats()
	// Assert on the researcher row's own line: the balance rows above it
	// also render MaskedAmount, so a whole-output check would pass even if
	// this row forgot to mask.
	rewardLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Pending Reward") {
			rewardLine = line
			break
		}
	}
	if rewardLine == "" {
		t.Fatalf("anonymous mode lost the researcher row, got:\n%s", out)
	}
	if strings.Contains(rewardLine, "12.35 GRC") || !strings.Contains(rewardLine, MaskedAmount) {
		t.Errorf("anonymous mode must mask the pending reward, got: %s", rewardLine)
	}
	if !strings.Contains(rewardLine, "245.75") {
		t.Errorf("anonymous mode should not hide magnitude, got: %s", rewardLine)
	}
}

// TestRenderStatsResearcherBadge covers the cruncher/investor indicator, which
// unlike the researcher row above must render in BOTH cases — that is the
// whole point of it. Without it an investor sees no researcher information at
// all and cannot tell "not crunching" from "misconfigured".
func TestRenderStatsResearcherBadge(t *testing.T) {
	const cpid = "8edc235ddceca9d6ad76d8e8fc9fe27e"
	m := Model{
		width:   100,
		loaded:  true,
		staking: StakingInfo{CPID: cpid},
	}
	out := m.renderStats()
	if !strings.Contains(out, "Researcher") || !strings.Contains(out, "cruncher") {
		t.Errorf("stats missing cruncher badge, got:\n%s", out)
	}
	// Truncated, not full: a 32-char CPID pushes the Total row past 80 columns.
	if !strings.Contains(out, ShortAddress(cpid)) {
		t.Errorf("stats should show the shortened CPID %q, got:\n%s", ShortAddress(cpid), out)
	}
	if strings.Contains(out, cpid) {
		t.Errorf("stats should not show the full CPID, got:\n%s", out)
	}

	// A placeholder CPID is not a CPID.
	m.staking = StakingInfo{CPID: "NONCRUNCHER"}
	out = m.renderStats()
	if !strings.Contains(out, "investor") {
		t.Errorf("non-cruncher should read as investor, got:\n%s", out)
	}
	if strings.Contains(out, "NONCRUNCHER") {
		t.Errorf("the daemon's placeholder word should never reach the UI, got:\n%s", out)
	}

	// Before the first getstakinginfo reply there is no CPID at all, and
	// "investor" would be a confident wrong answer for every cruncher during
	// the startup sequence. Both label and value must be absent, not blank.
	m.staking = StakingInfo{}
	out = m.renderStats()
	if strings.Contains(out, "Researcher") || strings.Contains(out, "investor") {
		t.Errorf("an unknown CPID must claim nothing, got:\n%s", out)
	}

	// Anonymous mode hides amounts, not identity: the CPID is public on-chain
	// data, and the magnitude two lines below is already treated that way.
	m.staking = StakingInfo{CPID: cpid}
	m.anonymous = true
	out = m.renderStats()
	if !strings.Contains(out, ShortAddress(cpid)) {
		t.Errorf("anonymous mode should keep the CPID visible, got:\n%s", out)
	}
}
