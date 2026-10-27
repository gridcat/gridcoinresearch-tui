// Column-accurate text measurement and clipping. Bound for sdk/chrome.
package ui

import (
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// StringWidth is runewidth.StringWidth with a fast path for text where no two
// runes combine into one visible character (grapheme cluster), so the width is
// just the sum of the rune widths. Rows are measured on every frame, and runewidth's grapheme
// segmentation dominated the render profile even though almost every string
// we draw is ASCII plus the odd −, … or status icon.
func StringWidth(s string) int {
	w := 0
	for _, r := range s {
		if !standaloneRune(r) {
			return runewidth.StringWidth(s)
		}
		w += runewidth.RuneWidth(r)
	}
	return w
}

// standaloneRune reports whether r can never join a neighbour into one
// grapheme cluster: Latin below the combining marks, and the punctuation,
// arrow, math, box-drawing and dingbat blocks minus their marks and format
// characters. Conservative on purpose; anything else takes the slow path.
func standaloneRune(r rune) bool {
	if r < 0x300 {
		return true
	}
	return r >= 0x2010 && r <= 0x2bff && !unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf)
}

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
	if StringWidth(Text) <= maxCols {
		return Text
	}
	return runewidth.Truncate(Text, maxCols, "…")
}

// FixedCell pads a table cell before it becomes a Seg. Measuring raw
// padded Text (rather than relying only on lipgloss.Style.Width) is essential:
// ClipSegments needs the same column widths that the rendered row occupies.
func FixedCell(Text string, width int, rightAlign bool) string {
	Text = SliceByCols(Text, 0, width)
	pad := width - StringWidth(Text)
	if pad <= 0 {
		return Text
	}
	spaces := strings.Repeat(" ", pad)
	if rightAlign {
		return spaces + Text
	}
	return Text + spaces
}
