// Tests for the terminal escape-sequence defence: the sanitizeTerminal helper
// itself, plus end-to-end render assertions proving daemon-authored escapes
// die before reaching the terminal while our own lipgloss styling survives.
// See rpc_test.go for a testing primer, and sanitizeTerminal in format.go for
// why on-chain poll data makes this an any-network-participant problem rather
// than a hostile-daemon one.
package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestSanitizeTerminal pins the exact strip set: every C0 control (ESC, CSI
// and OSC introducers, BEL, NUL, and also newline/tab since every render site
// is a single fixed-width line), DEL, and the C1 range — while ordinary text
// and multi-byte UTF-8 outside those ranges pass through untouched. The
// C1 case is the one a byte-wise rewrite would regress: those codepoints are
// two bytes in UTF-8, and corrupting them would also mangle innocent é/…/⚠.
func TestSanitizeTerminal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ESC+CSI colour", "a\x1b[31mb", "a[31mb"},
		{"OSC 52 clipboard with BEL", "x\x1b]52;c;cGF5bG9hZA==\x07y", "x]52;c;cGF5bG9hZA==y"},
		{"NUL", "a\x00b", "ab"},
		{"DEL", "a\x7fb", "ab"},
		{"C1 CSI (U+009B)", "a\u009b2Jb", "a2Jb"},
		{"newline", "row1\nrow2", "row1row2"},
		{"tab", "col1\tcol2", "col1col2"},
		{"clean ASCII is a no-op", "S9jd8jK7 label 12.34 GRC", "S9jd8jK7 label 12.34 GRC"},
		{"multi-byte text survives", "é … ⚠ 日本語", "é … ⚠ 日本語"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeTerminal(tc.in); got != tc.want {
				t.Errorf("sanitizeTerminal(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRenderPollsScreenStripsEscapes proves the fix where it matters most: a
// poll title is on-chain data any network participant can author, and it must
// not be able to smuggle an ESC into the frame. The regression to guard
// against is someone rendering the title before (or instead of) sanitizing —
// truncate() won't catch it, because control runes have display width 0.
func TestRenderPollsScreenStripsEscapes(t *testing.T) {
	m := Model{
		width:             100,
		height:            24,
		mode:              modePolls,
		pollsLoaded:       true,
		pollsShowFinished: true,
		polls: []Poll{
			// Clear-screen CSI plus an OSC title-set, mid-title.
			{Title: "Vote \x1b[2Jnow \x1b]0;pwned\x07please", ID: "a", WeightType: "Magnitude", Expiration: "12-31-2999 23:59:59", Votes: 3},
		},
		pollResults:       map[string]PollResult{},
		pollResultPending: map[string]bool{},
	}

	out := m.View()
	if strings.Contains(out, "\x1b[2J") {
		t.Errorf("poll title's CSI clear-screen survived into the frame:\n%s", out)
	}
	if strings.Contains(out, "\x1b]") {
		t.Errorf("poll title's OSC introducer survived into the frame:\n%s", out)
	}
	// The legitimate words around the escapes must still be there — we drop
	// control runes, not the text they were hiding in.
	if !strings.Contains(out, "Vote") || !strings.Contains(out, "now") {
		t.Errorf("sanitizing ate legitimate title text, got:\n%s", out)
	}
}

// TestRenderTxRowStripsEscapes is the transaction-side twin: tx.Address comes
// from the daemon and lands in a fixed-width column, so an ESC in it must be
// dropped before ShortAddress measures the string. The printable remainder of
// the sequence ("[2J…") staying visible is deliberate — we strip control
// runes only, we don't try to parse and delete whole sequences.
func TestRenderTxRowStripsEscapes(t *testing.T) {
	tx := Transaction{Category: "receive", Address: "S\x1b[2JAbc9", TxID: "t1", Amount: 1.5, Confirmations: 10}
	out := renderTxRow(tx, false, "")
	if strings.Contains(out, "\x1b[2J") {
		t.Errorf("address's CSI clear-screen survived into the row:\n%s", out)
	}
	if !strings.Contains(out, "[2JAbc9") {
		t.Errorf("expected the escape's printable remainder in the address column, got:\n%s", out)
	}
}

// TestSanitizedRenderKeepsOwnStyling is the over-stripping guard — the single
// most likely way to get this fix wrong. We strip the daemon's escapes, not
// lipgloss's: sanitizing a value AFTER our own styles rendered it would erase
// the UI's colours, so this asserts a frame with a hostile title still
// carries our SGR escapes while the hostile CSI is gone. Tests run without a
// TTY, where lipgloss detects a colourless profile and emits no escapes at
// all, so the profile is forced (and restored) to make our styling visible.
func TestSanitizedRenderKeepsOwnStyling(t *testing.T) {
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(orig)

	m := Model{
		width:             100,
		height:            24,
		mode:              modePolls,
		pollsLoaded:       true,
		pollsShowFinished: true,
		polls: []Poll{
			{Title: "Sneaky \x1b[2Jpoll", ID: "a", WeightType: "Balance", Expiration: "12-31-2999 23:59:59", Votes: 1},
		},
		pollResults:       map[string]PollResult{},
		pollResultPending: map[string]bool{},
	}

	out := m.View()
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("frame carries no SGR escapes at all — our own styling got stripped too:\n%q", out)
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Errorf("daemon CSI survived alongside our styling:\n%q", out)
	}
}
