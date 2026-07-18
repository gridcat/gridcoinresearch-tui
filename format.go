// Small utilities for turning raw numbers and timestamps into things a human
// can read. Kept in one file so render code (view.go) stays focused on
// layout rather than formatting details.
package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// GRC's base unit is 1e-8 GRC (like Bitcoin's satoshi), so the maximum
// meaningful precision is 8 decimal places. Detail views use this; glance
// views use 2 decimals to stay compact.
const grcDetailDecimals = 8

// MaskedAmount is the placeholder shown instead of real amounts when
// anonymous mode is active.
const MaskedAmount = "••••••  GRC"

// FormatGRC renders an amount for table/glance display: 2 decimals, always
// with a sign prefix ("+12.34 GRC", "−100.00 GRC", "0.00 GRC"). Uses a
// Unicode minus sign so negative amounts visually line up with the plus.
func FormatGRC(amount float64) string {
	sign := ""
	if amount < 0 {
		sign = "−"
		amount = -amount
	} else if amount > 0 {
		sign = "+"
	}
	return fmt.Sprintf("%s%s GRC", sign, groupThousands(amount, 2))
}

// FormatGRCPlain is the no-sign variant for contexts where the sign is
// implied (balance, stake amount, "available" labels).
func FormatGRCPlain(amount float64) string {
	return groupThousands(math.Abs(amount), 2) + " GRC"
}

// FormatGRCFull is the detail-view variant: full 8-decimal precision plus a
// sign prefix. Used in the transaction detail modal and the send-confirm
// dialog where trimming to 2 decimals would hide real value.
func FormatGRCFull(amount float64) string {
	sign := ""
	if amount < 0 {
		sign = "−"
		amount = -amount
	} else if amount > 0 {
		sign = "+"
	}
	return fmt.Sprintf("%s%s GRC", sign, groupThousands(amount, grcDetailDecimals))
}

// FormatGRCFullPlain is FormatGRCFull without a sign prefix.
func FormatGRCFullPlain(amount float64) string {
	return groupThousands(math.Abs(amount), grcDetailDecimals) + " GRC"
}

// groupThousands formats a float with the requested decimal precision and
// then inserts thousands separators so "1234567.89" becomes "1,234,567.89".
// Formatting via strconv first lets Go handle rounding correctly before we
// poke commas into the string.
func groupThousands(n float64, decimals int) string {
	formatted := strconv.FormatFloat(n, 'f', decimals, 64)
	return insertThousandsSep(formatted)
}

// groupThousandsInt64 is a precision-safe int64 variant for block heights
// and similar large counters. Avoiding the float64 intermediate matters
// because float64 starts losing integer precision beyond 2^53.
func groupThousandsInt64(n int64) string {
	return insertThousandsSep(strconv.FormatInt(n, 10))
}

// insertThousandsSep walks the integer part of an already-formatted number
// string and writes a comma every three digits from the right. It handles a
// possible decimal point and leading minus sign so both int and float
// callers can share the same helper.
func insertThousandsSep(s string) string {
	intPart := s
	fracPart := ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart = s[:dot]
		fracPart = s[dot:]
	}
	negative := false
	if strings.HasPrefix(intPart, "-") {
		negative = true
		intPart = intPart[1:]
	}
	// strings.Builder avoids repeated string allocations while we assemble
	// the output. Grow hints the builder roughly how big the result will be.
	var b strings.Builder
	b.Grow(len(s) + len(intPart)/3)
	if negative {
		b.WriteByte('-')
	}
	for i := 0; i < len(intPart); i++ {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(intPart[i])
	}
	b.WriteString(fracPart)
	return b.String()
}

// FormatRelativeTime renders a unix timestamp as "just now", "3m ago",
// "2h ago", "4d ago" or a bare date — whichever is most useful at that age.
func FormatRelativeTime(ts int64) string {
	if ts <= 0 {
		return "—"
	}
	diff := time.Since(time.Unix(ts, 0))
	switch {
	case diff < 5*time.Second:
		return "just now"
	case diff < time.Minute:
		return fmt.Sprintf("%ds ago", int(diff.Seconds()))
	case diff < time.Hour:
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	case diff < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(diff.Hours()/24))
	default:
		// Go's reference time is always Mon Jan 2 15:04:05 MST 2006 — every
		// digit in that phrase is a placeholder the formatter interprets.
		return time.Unix(ts, 0).Format("2006-01-02")
	}
}

// FormatDuration renders a time.Duration as a short "1h59m" / "45m30s" /
// "12s" string for the wallet unlock countdown.
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// ShortAddress truncates a wallet address for table display ("S12abc…XYZ").
// Detail views should render the full address instead.
func ShortAddress(addr string) string {
	if len(addr) <= 12 {
		return addr
	}
	return addr[:6] + "…" + addr[len(addr)-3:]
}

// FormatStakeETA renders the seconds-until-next-expected-stake value from
// getstakinginfo.expectedtime as something a human can read at a glance.
// Units are picked by magnitude: a few minutes, a few hours and minutes,
// or days and hours for long predictions.
func FormatStakeETA(seconds int64) string {
	if seconds <= 0 {
		return "—"
	}
	d := time.Duration(seconds) * time.Second
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh %dm", h, m)
	case d < 30*24*time.Hour:
		days := int(d.Hours()) / 24
		hours := int(d.Hours()) % 24
		return fmt.Sprintf("%dd %dh", days, hours)
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	default:
		years := int(d.Hours()) / (365 * 24)
		return fmt.Sprintf(">%dy", years)
	}
}

// pollTimeLayout matches gridcoinresearchd's TimestampToHRDate output
// ("%m-%d-%Y %H:%M:%S"), which is emitted in UTC. Go's reference time spells
// that same layout as "01-02-2006 15:04:05".
const pollTimeLayout = "01-02-2006 15:04:05"

// ParsePollTime parses a gridcoinresearchd HR date string (poll
// expiration/timestamp) as UTC. Returns the zero time on any parse failure, so
// callers can treat "couldn't read it" the same as "unknown" rather than
// crashing if a future daemon changes the format.
func ParsePollTime(s string) time.Time {
	t, err := time.Parse(pollTimeLayout, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t // a zoneless layout parses as UTC already
}

// PollExpired reports whether a poll's expiration has passed, derived from the
// HR date string. An unparseable/empty expiration returns false (treat as
// still active) so a format surprise never hides live polls.
func PollExpired(expiration string) bool {
	return pollExpired(ParsePollTime(expiration))
}

// pollExpired is the parsed-time form of PollExpired, so a caller that already
// has the parsed expiration (e.g. renderPollRow) doesn't re-parse the string.
func pollExpired(exp time.Time) bool {
	return !exp.IsZero() && time.Now().After(exp)
}

// FormatPollTimeLeft renders how long until a poll closes as a compact
// "3d 4h" / "5h 2m" / "12m" string, "ended" once past, or "—" if the
// expiration string didn't parse. Reuses FormatStakeETA's unit picking so poll
// countdowns read the same as the stake ETA elsewhere.
func FormatPollTimeLeft(expiration string) string {
	return formatPollTimeLeft(ParsePollTime(expiration))
}

// formatPollTimeLeft is the parsed-time form of FormatPollTimeLeft, so a caller
// with the expiration already parsed avoids a second time.Parse.
func formatPollTimeLeft(exp time.Time) string {
	if exp.IsZero() {
		return "—"
	}
	remaining := time.Until(exp)
	if remaining <= 0 {
		return "ended"
	}
	return FormatStakeETA(int64(remaining.Seconds()))
}

// formatCompactNumber renders a (possibly large) number as a short "10.2M" /
// "4.1K" / "512" string for the poll detail popup's per-choice weight column,
// where the exact figure matters less than the order of magnitude. Weights are
// magnitude- or balance-derived depending on the poll, so it carries no unit.
func formatCompactNumber(n float64) string {
	abs := math.Abs(n)
	switch {
	case abs >= 1e9:
		return strconv.FormatFloat(n/1e9, 'f', 1, 64) + "B"
	case abs >= 1e6:
		return strconv.FormatFloat(n/1e6, 'f', 1, 64) + "M"
	case abs >= 1e3:
		return strconv.FormatFloat(n/1e3, 'f', 1, 64) + "K"
	default:
		return strconv.FormatFloat(n, 'f', 0, 64)
	}
}

// formatVoteCount renders a poll response's vote count to two decimals. The
// daemon may report a fraction (a voter can split one vote across choices in a
// multiple-choice poll), often with long repeating decimals like
// 5.833333333333333, so we fix the precision at two places: "153.50", "5.83".
func formatVoteCount(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// ShortWeightType abbreviates a poll's weight_type for the narrow list column.
// The full strings come from Poll::WeightTypeToString in the daemon.
func ShortWeightType(wt string) string {
	switch strings.ToLower(wt) {
	case "magnitude":
		return "Mag"
	case "balance":
		return "Bal"
	case "magnitude+balance":
		return "M+B"
	case "cpid count":
		return "CPID"
	case "participant count":
		return "Part"
	case "":
		return "—"
	default:
		return wt
	}
}

// TxStatusKind is a type-safe enum for the transaction status bucket we
// render in the UI. Using an integer enum (rather than raw strings) means
// the compiler catches typos in switch statements that would otherwise
// silently fall through to the `default` branch.
type TxStatusKind int

const (
	TxStatusUpcoming  TxStatusKind = iota // in mempool, 0 confirmations
	TxStatusIncoming                      // receive, 1..confirmedThreshold-1 confirmations
	TxStatusSending                       // send, 1..confirmedThreshold-1 confirmations
	TxStatusConfirmed                     // >= confirmedThreshold confirmations
	TxStatusStake                         // staking reward (category generate/immature)
)

// TxStatus couples the enum with a human label and a single-character icon.
// The label is what we show in the table; the icon is a compact visual cue.
type TxStatus struct {
	Kind  TxStatusKind
	Label string
	Icon  string
}

// confirmedThreshold is the depth at which we stop saying "incoming" and
// start saying "confirmed". 6 matches the bitcoin-ecosystem default.
const confirmedThreshold = 6

// ClassifyTransaction maps the raw RPC category + confirmation depth to one
// of our five status buckets. Order matters: stakes are detected first so
// an un-confirmed stake reward doesn't show up as "upcoming".
func ClassifyTransaction(tx Transaction) TxStatus {
	if tx.Category == "generate" || tx.Category == "immature" {
		return TxStatus{Kind: TxStatusStake, Label: "stake", Icon: "✦"}
	}
	if tx.Confirmations <= 0 {
		return TxStatus{Kind: TxStatusUpcoming, Label: "upcoming", Icon: "●"}
	}
	if tx.Confirmations < confirmedThreshold {
		if tx.Category == "send" {
			return TxStatus{Kind: TxStatusSending, Label: "sending", Icon: "◐"}
		}
		return TxStatus{Kind: TxStatusIncoming, Label: "incoming", Icon: "◐"}
	}
	return TxStatus{Kind: TxStatusConfirmed, Label: "confirmed", Icon: "✓"}
}
