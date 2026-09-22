package tui

import (
	"strings"
	"testing"

	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
)

// TestUnconfirmedReceivedConfirmationBoundary prevents received funds from
// disappearing between the TUI's Unconfirmed and Balance totals. Gridcoin's
// wallet keeps an external receive out of GetBalance() until confirmation 10,
// so the TUI must continue deriving it as unconfirmed through confirmation 9.
func TestUnconfirmedReceivedConfirmationBoundary(t *testing.T) {
	for _, tc := range []struct {
		confirmations int64
		want          float64
	}{
		{confirmations: 0, want: 11500},
		{confirmations: 6, want: 11500},
		{confirmations: 9, want: 11500},
		{confirmations: 10, want: 0},
	} {
		m := Model{txs: []rpc.Transaction{{
			Category:      "receive",
			Amount:        11500,
			Confirmations: tc.confirmations,
		}}}
		if got := m.unconfirmedReceived(); got != tc.want {
			t.Errorf("confirmations %d: unconfirmedReceived() = %.2f, want %.2f",
				tc.confirmations, got, tc.want)
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
		staking: rpc.StakingInfo{
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

	m.staking = rpc.StakingInfo{}
	out = m.renderStats()
	if strings.Contains(out, "Magnitude") || strings.Contains(out, "Pending Reward") {
		t.Errorf("investor should have no researcher row, got:\n%s", out)
	}

	m.staking = rpc.StakingInfo{Magnitude: &mag, PendingReward: &pending}
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
	if strings.Contains(rewardLine, "12.35 GRC") || !strings.Contains(rewardLine, format.MaskedAmount) {
		t.Errorf("anonymous mode must mask the pending reward, got: %s", rewardLine)
	}
	if !strings.Contains(rewardLine, "245.75") {
		t.Errorf("anonymous mode should not hide magnitude, got: %s", rewardLine)
	}
}

// TestRenderStatsResearcherBadge covers the cruncher/investor indicator, which
// unlike the researcher row above must render in BOTH cases, which is the
// whole point of it. Without it an investor sees no researcher information at
// all and cannot tell "not crunching" from "misconfigured".
func TestRenderStatsResearcherBadge(t *testing.T) {
	const cpid = "8edc235ddceca9d6ad76d8e8fc9fe27e"
	m := Model{
		width:   100,
		loaded:  true,
		staking: rpc.StakingInfo{CPID: cpid},
	}
	out := m.renderStats()
	if !strings.Contains(out, "Researcher") || !strings.Contains(out, "cruncher") {
		t.Errorf("stats missing cruncher badge, got:\n%s", out)
	}
	// Truncated, not full: a 32-char CPID pushes the Total row past 80 columns.
	if !strings.Contains(out, format.ShortAddress(cpid)) {
		t.Errorf("stats should show the shortened CPID %q, got:\n%s", format.ShortAddress(cpid), out)
	}
	if strings.Contains(out, cpid) {
		t.Errorf("stats should not show the full CPID, got:\n%s", out)
	}

	// A placeholder CPID is not a CPID.
	m.staking = rpc.StakingInfo{CPID: "NONCRUNCHER"}
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
	m.staking = rpc.StakingInfo{}
	out = m.renderStats()
	if strings.Contains(out, "Researcher") || strings.Contains(out, "investor") {
		t.Errorf("an unknown CPID must claim nothing, got:\n%s", out)
	}

	// Anonymous mode hides amounts, not identity: the CPID is public on-chain
	// data, and the magnitude two lines below is already treated that way.
	m.staking = rpc.StakingInfo{CPID: cpid}
	m.anonymous = true
	out = m.renderStats()
	if !strings.Contains(out, format.ShortAddress(cpid)) {
		t.Errorf("anonymous mode should keep the CPID visible, got:\n%s", out)
	}
}
