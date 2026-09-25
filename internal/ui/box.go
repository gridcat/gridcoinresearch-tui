// Package ui holds the drawing primitives that know nothing about this
// wallet: boxes, text measurement, styled segments and list windowing. They
// take values and return strings.
//
// The split into files is deliberate. docs/plugins-plan.md Part 2 gives the
// future grc-tui-sdk two homes for this code: "sdk/chrome" (Header,
// TitledBox, footer, status line, truncate) and "sdk/engine" (layout,
// rendering, selection, scrolling). So box.go and text.go are the chrome
// half and segment.go and list.go are the engine half. Lifting them later
// should be a move, not a rewrite.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
)

// Box drawing. Bound for sdk/chrome.
// TitledBox renders content inside a bordered box whose top edge carries the
// title, the way btop draws its panels:
//
//	╭─ Transactions ───────────────╮
//	│ ▸ 12:03  +1.25   confirmed   │
//	╰──────────────────────────────╯
//
// Style is a bordered Style with Width (and optionally Height) already
// applied; the helper draws the top edge itself and lets Lipgloss draw the
// other three sides, so padding, width and height keep their usual meaning and
// the outer size is the same as a plain box. titleStyle colours the label,
// which lets callers follow focus (styleTitle when idle, styleAccent when the
// panel has focus). A title too wide for the edge is truncated; one that can't
// fit at all leaves a plain edge behind.
func TitledBox(Style lipgloss.Style, titleStyle lipgloss.Style, title, content string) string {
	return TitledBoxFooter(Style, titleStyle, title, "", content)
}

// TitledBoxFooter is TitledBox with a second, muted label right-aligned in
// the bottom edge, for status that shouldn't cost a content row (a "3/30"
// position counter, say). An empty footer draws the plain bottom edge.
//
//	╭─ Transactions ───────────────╮
//	│ ▸ 12:03  +1.25   confirmed   │
//	╰──────────────────────── 3/30 ─╯
func TitledBoxFooter(Style lipgloss.Style, titleStyle lipgloss.Style, title, footer, content string) string {
	b := Style.GetBorderStyle()
	edge := lipgloss.NewStyle().Foreground(Style.GetBorderTopForeground())
	bodyStyle := Style.BorderTop(false)
	if footer != "" {
		bodyStyle = bodyStyle.BorderBottom(false)
	}
	body := bodyStyle.Render(content)
	// Measure the body rather than trusting Width(): content wider than the
	// Style's width pushes the box out, and the top edge has to follow it or
	// the box loses its right corner.
	w := lipgloss.Width(body)
	if w < 2 {
		w = 2
	}
	out := edgeWithLabel(edge, titleStyle, b.TopLeft, b.Top, b.TopRight, title, w, false) + "\n" + body
	if footer != "" {
		out += "\n" + edgeWithLabel(edge, theme.Muted, b.BottomLeft, b.Bottom, b.BottomRight, footer, w, true)
	}
	return out
}

// edgeWithLabel draws one horizontal border edge w columns wide carrying
// label, flush left ("╭─ label ───╮") or flush right ("╰─── label ─╯").
//
// "╭─ " + label + " ─╮" spends 6 columns on chrome before a label fits.
// Truncate measures with go-runewidth and the edge is laid out with
// lipgloss.Width; the two disagree on a few graphemes (an emoji carrying a
// variation selector counts 1 and 2), so the fill is re-checked against the
// label lipgloss will actually draw. A label that still doesn't fit gives up
// its space rather than pushing the closing corner off the end.
func edgeWithLabel(edge, labelStyle lipgloss.Style, left, line, right, text string, w int, alignRight bool) string {
	label := Truncate(text, w-6)
	fill := 0
	if label != "" {
		label = " " + label + " "
		fill = w - 3 - lipgloss.Width(label)
	}
	if fill < 1 {
		return edge.Render(left + strings.Repeat(line, w-2) + right)
	}
	if alignRight {
		return edge.Render(left+strings.Repeat(line, fill)) +
			labelStyle.Render(label) +
			edge.Render(line+right)
	}
	return edge.Render(left+line) +
		labelStyle.Render(label) +
		edge.Render(strings.Repeat(line, fill)+right)
}

// ModalBox renders a dialog the way TitledBox renders a panel: a
// double-bordered box of the given width carrying its title in the top edge.
// Callers pass the body alone: the title is not a content row, and the box's
// top padding supplies the blank line beneath it.
func ModalBox(width int, title, body string) string {
	Style := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(theme.ColorAccent).
		Padding(1, 2).
		Width(width)
	return TitledBox(Style, theme.Title, title, body)
}
