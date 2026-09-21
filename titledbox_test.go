// Tests for titledBox, the btop-style panel box that carries its title in the
// top border edge. The shape of that edge is load-bearing: it has to stay
// exactly as wide as a plain box at every width, keep both corners, and never
// let a long title push the closing corner off the end. See rpc_test.go for a
// testing primer.
package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// boxStyleOfWidth is the shape every panel passes to titledBox: a rounded
// border with the family's 1-column horizontal padding.
func boxStyleOfWidth(w int) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(w)
}

// TestTitledBoxTopEdge pins the top edge at the widths that matter: roomy,
// exactly wide enough for the whole title, wide enough only for a truncated
// one, and too narrow for any title at all.
func TestTitledBoxTopEdge(t *testing.T) {
	cases := []struct {
		name  string
		width int // content width handed to the style
		want  string
	}{
		{"roomy", 30, "╭─ Transactions ───────────────╮"},
		// 18 outer columns is the exact fit: ╭─ + " Transactions " + ─ + ╮.
		{"exact fit", 16, "╭─ Transactions ─╮"},
		{"truncated", 12, "╭─ Transac… ─╮"},
		// 6 outer columns leaves nothing for a label, so the edge goes plain
		// rather than losing a corner.
		{"too narrow", 4, "╭────╮"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// One narrow content column, so the box is exactly as wide as the
			// style asks for and the edge maths is the only thing under test.
			out := titledBox(boxStyleOfWidth(tc.width), styleTitle, "Transactions", "x")
			top := strings.SplitN(out, "\n", 2)[0]
			if top != tc.want {
				t.Fatalf("top edge = %q, want %q", top, tc.want)
			}
			if got, want := lipgloss.Width(top), tc.width+2; got != want {
				t.Fatalf("top edge width = %d, want %d", got, want)
			}
		})
	}
}

// TestTitledBoxWideGlyphTitle guards the top edge against a title whose width
// go-runewidth and lipgloss disagree about (an emoji with a variation selector
// counts 1 column for the former, 2 for the latter). The edge has to stay
// exactly as wide as the box either way, and must never ask strings.Repeat for
// a negative count — a panic there takes the whole TUI down mid-render.
func TestTitledBoxWideGlyphTitle(t *testing.T) {
	for _, width := range []int{4, 5, 7, 10, 20, 40} {
		style := boxStyleOfWidth(width)
		out := titledBox(style, styleTitle, "❤️❤️❤️ hearts", "x")
		top := strings.SplitN(out, "\n", 2)[0]
		if got, want := lipgloss.Width(top), lipgloss.Width(style.Render("x")); got != want {
			t.Errorf("width %d: top edge is %d columns, box is %d", width, got, want)
		}
		if !strings.HasPrefix(top, "╭") || !strings.HasSuffix(top, "╮") {
			t.Errorf("width %d: top edge lost a corner: %q", width, top)
		}
	}
}

// TestTitledBoxOuterSizeMatchesPlainBox is the guard for the height
// arithmetic: swapping a plain box for a titled one must not change the
// rendered size, so callers keep their existing Width/Height maths.
func TestTitledBoxOuterSizeMatchesPlainBox(t *testing.T) {
	content := "one\ntwo\nthree"
	for _, height := range []int{3, 5, 8} {
		style := boxStyleOfWidth(40).Height(height)
		plain := style.Render(content)
		titled := titledBox(style, styleTitle, "Transactions", content)

		if got, want := lipgloss.Height(titled), lipgloss.Height(plain); got != want {
			t.Fatalf("height(%d): titled box is %d rows, plain box is %d", height, got, want)
		}
		if got, want := lipgloss.Width(titled), lipgloss.Width(plain); got != want {
			t.Fatalf("height(%d): titled box is %d columns, plain box is %d", height, got, want)
		}
	}
}

// TestTitledBoxTitleFollowsFocus checks the label is drawn with the style the
// caller passed, which is how a focused panel gets an accent-coloured title to
// match its accent-coloured border.
func TestTitledBoxTitleFollowsFocus(t *testing.T) {
	// Rendering strips colour under the Ascii profile tests run with, so ask
	// for a colour profile and put it back afterwards.
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(previous)

	style := boxStyleOfWidth(30)
	idle := titledBox(style, styleTitle, "Transactions", "row")
	focused := titledBox(styleBorderFocused.Padding(0, 1).Width(30), styleAccent, "Transactions", "row")

	if !strings.Contains(idle, styleTitle.Render(" Transactions ")) {
		t.Error("idle box does not carry a styleTitle label")
	}
	if !strings.Contains(focused, styleAccent.Render(" Transactions ")) {
		t.Error("focused box does not carry a styleAccent label")
	}
	if idle == focused {
		t.Error("focused and idle boxes render identically")
	}
}
