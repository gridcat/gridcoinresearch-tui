// Styled row segments: the unit a scrollable row is built from.
// Bound for sdk/engine.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/mattn/go-runewidth"
)

// Seg is one coloured run of an address row. We keep rows as a list of
// (plain Text, Style) pairs rather than a single pre-rendered string so the
// horizontal-scroll window can slice them by visual column and still Style
// each visible piece. Slicing an already-rendered ANSI string by column is
// what the pinned x/ansi can't do (it only truncates from the right).
type Seg struct {
	Text  string
	Style lipgloss.Style
}

// SegmentsWidth is the total visual column count of a row, used to clamp the
// horizontal scroll so it can't pan past the longest line.
func SegmentsWidth(segs []Seg) int {
	w := 0
	for _, s := range segs {
		w += runewidth.StringWidth(s.Text)
	}
	return w
}

// ClipSegments renders the row through a horizontal window [offset, offset+
// width) of visual columns, styling each visible slice. A muted ‹ marks
// content hidden off the left edge and › content hidden off the right; each
// marker reserves one column so the result never exceeds width (and so never
// wraps to a second line).
func ClipSegments(segs []Seg, offset, width int) string {
	if width < 1 {
		return ""
	}
	total := SegmentsWidth(segs)

	avail := width
	left := ""
	if offset > 0 {
		left = theme.Muted.Render("‹")
		avail--
	}
	right := ""
	if total-offset > avail {
		right = theme.Muted.Render("›")
		avail--
	}
	if avail < 0 {
		avail = 0
	}

	end := offset + avail
	var b strings.Builder
	b.WriteString(left)
	col := 0
	for _, seg := range segs {
		segStart := col
		segEnd := col + runewidth.StringWidth(seg.Text)
		col = segEnd
		if segEnd <= offset || segStart >= end {
			continue
		}
		lo := offset
		if segStart > lo {
			lo = segStart
		}
		hi := end
		if segEnd < hi {
			hi = segEnd
		}
		b.WriteString(seg.Style.Render(SliceByCols(seg.Text, lo-segStart, hi-segStart)))
	}
	b.WriteString(right)
	return b.String()
}

// FillBackground paints the selection background across an already-rendered
// line and pads it to width columns, so a highlighted row is coloured edge to
// edge. We can't just wrap the line in a background Style: every Style reset
// inside it (lipgloss ends each coloured run with one) would clear the
// background, leaving only the start tinted.
// Instead we re-assert the background escape immediately after every reset and
// fill the remainder with background spaces. The escape sequences are derived
// from lipgloss itself (via a NUL-delimited probe) so they match whatever
// colour profile is active; on a no-colour terminal the probe yields no escape
// and the line passes through unchanged.
func FillBackground(line string, width int) string {
	probe := lipgloss.NewStyle().Background(theme.ColorRowSelected).Render("\x00")
	i := strings.IndexByte(probe, 0)
	if i <= 0 {
		return line // no-colour profile: nothing to paint
	}
	open, reset := probe[:i], probe[i+1:]

	body := open + strings.ReplaceAll(line, reset, reset+open)
	if pad := width - lipgloss.Width(line); pad > 0 {
		body += strings.Repeat(" ", pad)
	}
	return body + reset
}
