// List windowing, progress bars and the spinner frames.
// Bound for sdk/engine.
package ui

import (
	"strings"

	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
)

// ListWindow computes the visible-row budget and scroll offset for a bordered
// scroll panel that is `height` rows tall and holds `total` rows. The chrome is
// the 2 border rows (the title lives in the top border edge, see TitledBox,
// not in the content), and the offset slides forward only, starting at 0 and
// advancing just enough to keep the cursor on the last visible row. Shared by
// renderTxList and renderPollsList so their scroll math can't drift.
func ListWindow(height, cursor, total int) (maxRows, offset int) {
	maxRows = height - 2
	if maxRows < 1 {
		maxRows = 1
	}
	if maxRows > total {
		maxRows = total
	}
	if cursor >= maxRows {
		offset = cursor - maxRows + 1
	}
	return maxRows, offset
}

// Bar draws a fixed-width proportional bar (filled █ + empty ░) for a
// fraction in [0,1], used by the poll detail popup's per-choice results
// breakdown. Out-of-range fractions are clamped.
func Bar(fraction float64, width int) string {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return theme.Accent.Render(strings.Repeat("█", filled)) + theme.Muted.Render(strings.Repeat("░", width-filled))
}

// SpinnerFrames is the Braille dot spinner used in the footer while
// RPC fetches are in flight. The set is 10 frames long so the spinner
// appears to rotate smoothly at spinnerInterval (250 ms per frame).
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
