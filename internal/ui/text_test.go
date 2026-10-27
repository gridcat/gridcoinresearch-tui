// Tests for StringWidth's fast path. It must agree with runewidth on every
// input, including the ones that have to fall back to grapheme segmentation.
package ui

import (
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestStringWidthMatchesRunewidth(t *testing.T) {
	for _, s := range []string{
		"",
		"SGrcPayeeAddr9x8y7z6w5v4u3t2s1rQpZ",
		"     −1,234.50 GRC",
		"SGrcPa…QpZ      ",
		"✓", "↓", "⚠", "›",
		"café",
		"e\u0301",              // e + combining acute: one cluster
		"✓\ufe0f",              // text symbol + emoji presentation selector
		"\U0001F44D\U0001F3FD", // emoji + skin-tone modifier
		"日本語",                  // wide CJK
		"a\u200db",             // zero-width joiner
		"\u20dd→\u20e3",        // combining enclosing marks inside the arrow block
		"tab\there",
	} {
		if got, want := StringWidth(s), runewidth.StringWidth(s); got != want {
			t.Errorf("StringWidth(%q) = %d, want %d", s, got, want)
		}
	}
}
