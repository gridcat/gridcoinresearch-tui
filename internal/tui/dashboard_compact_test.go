package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
)

// worstCaseModel is a dashboard with every optional stats row switched on
// (unconfirmed, immature stake, researcher reward and magnitude, an unlock
// countdown) and more transactions and addresses than any window shows: the
// tallest the dashboard can get before an error line.
func worstCaseModel(width, height int) Model {
	mag, pend := 123.45, 12.5
	until := time.Now().Add(time.Hour).Unix()
	m := Model{width: width, height: height, focusedArea: focusTx,
		loaded: true, txsLoaded: true, addrsLoaded: true, peersLoaded: true,
		addrMine: map[string]bool{}}
	m.wallet = rpc.WalletInfo{Balance: 123456.78, UnconfirmedBalance: 12, ImmatureBalance: 3, Stake: 10, UnlockedUntil: &until}
	m.staking.Staking = true
	m.staking.ExpectedTime = 86400
	m.staking.Magnitude, m.staking.PendingReward = &mag, &pend
	m.staking.CPID = "0123456789abcdef0123456789abcdef"
	m.chain.Blocks = 3500000
	m.peersTotal, m.peersOut = 8, 8
	addr := "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ"
	for i := 0; i < 40; i++ {
		m.txs = append(m.txs, rpc.Transaction{Category: "send", Amount: -1234.5 - float64(i), Address: addr, TxID: fmt.Sprint(i), Confirmations: 100, Time: time.Now().Add(-time.Duration(i) * time.Hour).Unix()})
		m.addresses = append(m.addresses, rpc.ReceivedAddress{Address: fmt.Sprintf("SMineAddr%02dxxxxxxxxxxxxxxxxxxxxxx", i), Label: "label"})
	}
	m.addresses = append(m.addresses, rpc.ReceivedAddress{Address: addr, Label: "stamp.gridcoin.club"})
	return m
}

// TestDashboardFitsTerminal checks that from minWidth×minHeight up the
// dashboard never renders taller or wider than the terminal, whichever list
// is showing and even with an error line. Bubble Tea drops the top of any
// frame taller than the screen, so a failure here means the header scrolls
// out of sight.
func TestDashboardFitsTerminal(t *testing.T) {
	for w := minWidth; w <= 140; w += 7 {
		for h := minHeight; h <= 44; h += 2 {
			for _, focus := range []focusArea{focusTx, focusAddr} {
				for _, walletErr := range []string{"", "connection refused: the daemon is not running"} {
					m := worstCaseModel(w, h)
					m.focusedArea = focus
					m.walletErr = walletErr
					out := m.View()
					if got := lipgloss.Height(out); got > h {
						t.Errorf("%dx%d focus=%d err=%v: height %d", w, h, focus, walletErr != "", got)
					}
					if got := lipgloss.Width(out); got > w {
						t.Errorf("%dx%d focus=%d err=%v: width %d", w, h, focus, walletErr != "", got)
					}
				}
			}
		}
	}
}

// TestCompactSwitch pins down when each layout is used: the compact one on a
// terminal under fullMinWidth×fullMinHeight, the full one from there up.
func TestCompactSwitch(t *testing.T) {
	for _, tc := range []struct {
		w, h    int
		compact bool
	}{
		{minWidth, minHeight, true},
		{fullMinWidth - 1, 60, true},   // tall but narrow
		{140, fullMinHeight - 1, true}, // wide but short
		{140, 45, false},
	} {
		m := worstCaseModel(tc.w, tc.h)
		if got := m.isCompact(); got != tc.compact {
			t.Errorf("%dx%d: isCompact = %v, want %v", tc.w, tc.h, got, tc.compact)
		}
		// The full layout always shows the program name in its header box;
		// the compact title never does.
		if got := !strings.Contains(m.View(), "gridcoinresearch-tui"); got != tc.compact {
			t.Errorf("%dx%d: rendered compact = %v, want %v", tc.w, tc.h, got, tc.compact)
		}
	}

	// At exactly fullMinWidth×fullMinHeight the layout depends on the wallet.
	// A plain one gets the full layout. The worst case stays compact, because
	// its wrapped stats would leave the lists only a row or two.
	plain := worstCaseModel(fullMinWidth, fullMinHeight)
	plain.wallet.UnconfirmedBalance, plain.wallet.ImmatureBalance, plain.wallet.Stake = 0, 0, 0
	plain.staking.Magnitude, plain.staking.PendingReward, plain.staking.CPID = nil, nil, ""
	if plain.isCompact() {
		t.Errorf("plain wallet at %dx%d should get the full layout", fullMinWidth, fullMinHeight)
	}
	if !worstCaseModel(fullMinWidth, fullMinHeight).isCompact() {
		t.Errorf("worst-case wallet at %dx%d should stay compact", fullMinWidth, fullMinHeight)
	}
}

// TestCompactStatsHidesZeros checks the compact stats box drops the amounts
// that are zero (Balance and Total excepted), keeps the statuses, and leaves
// Difficulty out.
func TestCompactStatsHidesZeros(t *testing.T) {
	m := worstCaseModel(minWidth, minHeight)
	m.wallet.UnconfirmedBalance, m.wallet.ImmatureBalance, m.wallet.Stake = 0, 0, 0
	m.staking.Magnitude, m.staking.PendingReward = nil, nil
	m.txs = nil // unconfirmedReceived counts from the tx list
	out := m.renderCompactStats()
	for _, want := range []string{"Bal", "Tot", "staking", "unlocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("compact stats lost %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"Unc", "Imm", "Stk", "Rwd", "Difficulty"} {
		if strings.Contains(out, gone) {
			t.Errorf("compact stats should hide %q:\n%s", gone, out)
		}
	}
	// Two amounts on the left, three statuses (staking, unlocked, cruncher)
	// on the right: the taller column sets the height, plus two borders.
	if got := lipgloss.Height(out); got != 5 {
		t.Errorf("stats box = %d rows, want 5", got)
	}
}
