package tui

import (
	"testing"

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
