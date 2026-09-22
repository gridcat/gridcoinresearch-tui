package format

import (
	"testing"
	"time"

	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
)

// TestClassifyTransaction is a table-driven test: each row is one scenario,
// we run them all through ClassifyTransaction and compare the enum output.
// Classifying on a typed enum (TxStatusKind) rather than a raw string means
// a typo in the comparison would fail to compile, which is much safer than
// string equality.
func TestClassifyTransaction(t *testing.T) {
	cases := []struct {
		name string
		tx   rpc.Transaction
		kind TxStatusKind
	}{
		{"pending receive", rpc.Transaction{Category: "receive", Confirmations: 0}, TxStatusUpcoming},
		{"pending send", rpc.Transaction{Category: "send", Confirmations: 0}, TxStatusUpcoming},
		{"shallow receive", rpc.Transaction{Category: "receive", Confirmations: 2}, TxStatusIncoming},
		{"shallow send", rpc.Transaction{Category: "send", Confirmations: 2}, TxStatusSending},
		{"six-confirmation receive", rpc.Transaction{Category: "receive", Confirmations: 6}, TxStatusIncoming},
		{"nine-confirmation receive", rpc.Transaction{Category: "receive", Confirmations: 9}, TxStatusIncoming},
		{"ten-confirmation receive", rpc.Transaction{Category: "receive", Confirmations: 10}, TxStatusConfirmed},
		{"deep receive", rpc.Transaction{Category: "receive", Confirmations: 100}, TxStatusConfirmed},
		{"deep send", rpc.Transaction{Category: "send", Confirmations: 100}, TxStatusConfirmed},
		{"stake", rpc.Transaction{Category: "generate", Confirmations: 50}, TxStatusStake},
		{"immature stake", rpc.Transaction{Category: "immature", Confirmations: 3}, TxStatusStake},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyTransaction(tc.tx).Kind; got != tc.kind {
				t.Errorf("got %v, want %v", got, tc.kind)
			}
		})
	}
}

// TestFormatGRC sanity-checks the short-form amount humaniser: sign prefix
// for nonzero, plain "0.00" for zero, thousands separator for big numbers.
func TestFormatGRC(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0.00 GRC"},
		{12.34, "+12.34 GRC"},
		{-100.00, "−100.00 GRC"},
		{12345.67, "+12,345.67 GRC"},
	}
	for _, tc := range cases {
		if got := FormatGRC(tc.in); got != tc.want {
			t.Errorf("FormatGRC(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSanitizeTerminal pins the exact strip set: every C0 control (ESC, CSI
// and OSC introducers, BEL, NUL, and also newline/tab since every render site
// is a single fixed-width line), DEL, and the C1 range, while ordinary text
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
			if got := SanitizeTerminal(tc.in); got != tc.want {
				t.Errorf("sanitizeTerminal(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestParsePollTime checks that gridcoinresearchd's "MM-DD-YYYY HH:MM:SS" HR
// dates parse as UTC, and that garbage yields the zero time rather than a panic.
func TestParsePollTime(t *testing.T) {
	got := ParsePollTime("03-09-2026 14:05:06")
	want := time.Date(2026, 3, 9, 14, 5, 6, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("ParsePollTime = %v, want %v", got, want)
	}
	if p := ParsePollTime("not a date"); !p.IsZero() {
		t.Errorf("ParsePollTime(garbage) = %v, want zero time", p)
	}
	if p := ParsePollTime(""); !p.IsZero() {
		t.Errorf("ParsePollTime(empty) = %v, want zero time", p)
	}
}

// TestPollExpired uses far-future / far-past dates so the result is stable
// regardless of when the test runs; an unparseable date is treated as active.
func TestPollExpired(t *testing.T) {
	if PollExpired("12-31-2999 23:59:59") {
		t.Error("a year-2999 poll should not be expired")
	}
	if !PollExpired("01-01-2000 00:00:00") {
		t.Error("a year-2000 poll should be expired")
	}
	if PollExpired("garbage") {
		t.Error("an unparseable expiration should be treated as active (false)")
	}
}

// TestFormatPollTimeLeft covers the three branches: still-open, ended, and
// unparseable.
func TestFormatPollTimeLeft(t *testing.T) {
	if got := FormatPollTimeLeft("01-01-2000 00:00:00"); got != "ended" {
		t.Errorf("past poll: got %q, want %q", got, "ended")
	}
	if got := FormatPollTimeLeft("bad"); got != "—" {
		t.Errorf("unparseable: got %q, want %q", got, "—")
	}
	// A poll far in the future should render some non-empty, non-"ended"
	// countdown (exact value depends on now, so we only assert the branch).
	if got := FormatPollTimeLeft("12-31-2999 23:59:59"); got == "ended" || got == "—" || got == "" {
		t.Errorf("future poll: got %q, want a countdown", got)
	}
}

// TestShortWeightType checks the abbreviations used in the narrow list column,
// including the empty and unknown fall-throughs.
func TestShortWeightType(t *testing.T) {
	cases := map[string]string{
		"Magnitude":         "Mag",
		"Balance":           "Bal",
		"Magnitude+Balance": "M+B",
		"CPID Count":        "CPID",
		"Participant Count": "Part",
		"":                  "—",
		"Something Else":    "Something Else",
	}
	for in, want := range cases {
		if got := ShortWeightType(in); got != want {
			t.Errorf("ShortWeightType(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestIsContractCandidate pins the shape-sniffing rule that stands in for the
// category listsinceblock never provides: a contract burn is a "send" with an
// empty address. Both halves matter: dropping the category check would swallow
// stakes (also address-less), dropping the address check would call every
// payment a contract.
func TestIsContractCandidate(t *testing.T) {
	cases := []struct {
		name string
		tx   rpc.Transaction
		want bool
	}{
		{"send with no address is a contract burn", rpc.Transaction{Category: "send", Address: ""}, true},
		{"send to a real address is a payment", rpc.Transaction{Category: "send", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ"}, false},
		{"stake has no address either", rpc.Transaction{Category: "generate", Address: ""}, false},
		{"immature stake has no address either", rpc.Transaction{Category: "immature", Address: ""}, false},
		{"receive is never a contract", rpc.Transaction{Category: "receive", Address: "SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsContractCandidate(tc.tx); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
