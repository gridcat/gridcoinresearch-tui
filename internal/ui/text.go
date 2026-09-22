// Column-accurate text measurement and clipping. Bound for sdk/chrome.
package ui

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// SliceByCols returns the substring of Text covering visual columns [lo, hi).
// A wide glyph that would straddle either boundary is dropped whole rather
// than split; zero-width runes (combining marks, variation selectors) stay
// attached to the base glyph they follow.
func SliceByCols(Text string, lo, hi int) string {
	if hi <= lo {
		return ""
	}
	var b strings.Builder
	col := 0
	for _, r := range Text {
		w := runewidth.RuneWidth(r)
		if w == 0 {
			if col > lo && col <= hi {
				b.WriteRune(r)
			}
			continue
		}
		if col >= hi {
			break
		}
		if col >= lo && col+w <= hi {
			b.WriteRune(r)
		}
		col += w
	}
	return b.String()
}

// Truncate shortens Text to at most maxCols display columns, appending an
// ellipsis when it had to cut. Width-aware (wide glyphs count as 2) so it
// never overflows a fixed-width column. maxCols <= 0 returns "".
func Truncate(Text string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	if runewidth.StringWidth(Text) <= maxCols {
		return Text
	}
	return runewidth.Truncate(Text, maxCols, "…")
}

// FixedCell pads a table cell before it becomes a Seg. Measuring raw
// padded Text (rather than relying only on lipgloss.Style.Width) is essential:
// ClipSegments needs the same column widths that the rendered row occupies.
func FixedCell(Text string, width int, rightAlign bool) string {
	Text = SliceByCols(Text, 0, width)
	pad := width - runewidth.StringWidth(Text)
	if pad <= 0 {
		return Text
	}
	spaces := strings.Repeat(" ", pad)
	if rightAlign {
		return spaces + Text
	}
	return Text + spaces
}
