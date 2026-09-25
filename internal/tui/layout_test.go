package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
)

func TestTxWindowOffsetMovesCursorBeforeScrollingUp(t *testing.T) {
	// This is the state after scrolling down: the selected row is at the
	// bottom of a five-row window beginning at index 6.
	m := Model{txs: make([]rpc.Transaction, 20), txCursor: 10, txOffset: 6}

	m.txCursor--
	if got := m.txWindowOffset(5); got != 6 {
		t.Errorf("moving up within the visible window offset = %d, want 6", got)
	}

	// Once the cursor moves above the window, the list follows it upward.
	m.txCursor = 5
	if got := m.txWindowOffset(5); got != 5 {
		t.Errorf("cursor above the window offset = %d, want 5", got)
	}
}

// TestTooSmallBanner checks the notice replaces the dashboard exactly below
// minWidth×minHeight, and that it never outgrows the terminal it is shown in,
// however tiny: a banner that wrapped would scroll itself off screen too.
func TestTooSmallBanner(t *testing.T) {
	for _, tc := range []struct {
		w, h   int
		banner bool
	}{
		{minWidth, minHeight, false},
		{minWidth - 1, minHeight, true},
		{minWidth, minHeight - 1, true},
		{20, 5, true},
		{3, 2, true},
		{1, 1, true},
	} {
		m := Model{width: tc.w, height: tc.h}
		out := m.View()
		if got := strings.Contains(out, "needs") || strings.Contains(out, "Terminal"); got != tc.banner && tc.w > 18 {
			t.Errorf("%dx%d: banner shown = %v, want %v", tc.w, tc.h, got, tc.banner)
		}
		if !tc.banner {
			continue
		}
		if w := lipgloss.Width(out); w > tc.w {
			t.Errorf("%dx%d: banner width %d overflows", tc.w, tc.h, w)
		}
		if h := lipgloss.Height(out); h > tc.h {
			t.Errorf("%dx%d: banner height %d overflows", tc.w, tc.h, h)
		}
	}
}

// TestTooSmallBlocksKeys guards against acting on a screen the user can't
// see: while the banner is up only Ctrl+C does anything.
func TestTooSmallBlocksKeys(t *testing.T) {
	m := Model{width: minWidth - 1, height: minHeight}
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if next.(Model).anonymous {
		t.Error("'a' toggled anonymous mode behind the banner")
	}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Error("Ctrl+C should still quit behind the banner")
	}
}
