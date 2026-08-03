// Tests for the colour scheme system: every scheme must repaint every
// colour-bearing style, schemes must be distinguishable from each other, an
// unknown name must degrade to the default, and applying a scheme has to be
// idempotent and reversible (the config modal can toggle the network at
// runtime). See rpc_test.go for a testing primer.
package main

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// colouredStyles returns the current foreground of every style that carries a
// scheme colour, keyed by name. Both the "schemes differ" and the "no style
// gets left behind" tests read the UI through this one lens, so a style added
// here is automatically covered by both.
func colouredStyles() map[string]lipgloss.TerminalColor {
	return map[string]lipgloss.TerminalColor{
		"styleLabel":         styleLabel.GetForeground(),
		"styleValue":         styleValue.GetForeground(),
		"styleMuted":         styleMuted.GetForeground(),
		"styleAccent":        styleAccent.GetForeground(),
		"styleTitle":         styleTitle.GetForeground(),
		"styleStatLabelA":    styleStatLabelA.GetForeground(),
		"styleStatLabelB":    styleStatLabelB.GetForeground(),
		"styleTxStatusCol":   styleTxStatusCol.GetForeground(),
		"stylePollWeightCol": stylePollWeightCol.GetForeground(),
		"stylePollStatCol":   stylePollStatCol.GetForeground(),
		"stylePollTimeCol":   stylePollTimeCol.GetForeground(),
		"configLabelStyle":   configLabelStyle.GetForeground(),
		"configLabelFocused": configLabelFocused.GetForeground(),
		"configValueFocused": configValueFocused.GetForeground(),
		"styleBorder":        styleBorder.GetBorderTopForeground(),
		"styleBorderFocused": styleBorderFocused.GetBorderTopForeground(),
		"styleGood":          styleGood.GetForeground(),
		"styleWarn":          styleWarn.GetForeground(),
		"styleBad":           styleBad.GetForeground(),
		"styleMainnetBadge":  styleMainnetBadge.GetForeground(),
		"styleTestnetBadge":  styleTestnetBadge.GetForeground(),
		"txKindIncoming":     txKindStyle[TxStatusIncoming].GetForeground(),
		"txKindUpcoming":     txKindStyle[TxStatusUpcoming].GetForeground(),
		"txKindConfirmed":    txKindStyle[TxStatusConfirmed].GetForeground(),
	}
}

// TestStatusColoursStayDistinct guards the one thing warming the status
// colours could break: good/warn/bad are the only cue for state rather than
// decoration, so on every scheme they must differ from each other. A scheme
// that collapses two of them makes "staking ● yes" and an error look alike.
func TestStatusColoursStayDistinct(t *testing.T) {
	for name, p := range schemes {
		if p.good == p.warn || p.good == p.bad || p.warn == p.bad {
			t.Errorf("scheme %q: status colours not mutually distinct (good=%v warn=%v bad=%v)",
				name, p.good, p.warn, p.bad)
		}
	}
}

// TestSchemesDifferVisibly is the whole point of the feature: a window on one
// scheme must not look like a window on another. Checks the chrome that
// actually differs rather than demanding every single style change, since
// schemes legitimately share the status colours.
func TestSchemesDifferVisibly(t *testing.T) {
	defer applyScheme(defaultScheme)

	applyScheme(defaultScheme)
	before := colouredStyles()

	applyScheme(testnetScheme)
	after := colouredStyles()

	for _, name := range []string{
		"styleBorder", "styleBorderFocused", "styleTitle",
		"styleLabel", "styleMuted", "styleValue", "styleAccent",
	} {
		if before[name] == after[name] {
			t.Errorf("%s identical across schemes (%v) — schemes are not distinguishable", name, before[name])
		}
	}
}

// TestEveryColouredStyleIsRebuilt guards the trap this design exists to close:
// a style built at declaration time captures its colour by value and would
// silently keep the first scheme's colours forever. Any style that survives a
// scheme switch unchanged is either genuinely shared or was forgotten in
// buildStyles — the assertion below forces that to be a deliberate choice.
func TestEveryColouredStyleIsRebuilt(t *testing.T) {
	defer applyScheme(defaultScheme)

	// Styles that legitimately hold the same colour on both schemes. "warn"
	// is already orange in the default scheme, so the orange scheme has no
	// reason to move it, and the testnet badge is orange by definition.
	shared := map[string]bool{
		"styleWarn":         true, // 214 on both
		"txKindUpcoming":    true, // styleWarn
		"styleTestnetBadge": true, // 214 on both
	}

	applyScheme(defaultScheme)
	before := colouredStyles()
	applyScheme(testnetScheme)
	after := colouredStyles()

	for name, got := range after {
		if shared[name] {
			continue
		}
		if before[name] == got {
			t.Errorf("%s did not change across schemes (%v) — is it missing from buildStyles?", name, got)
		}
	}
}

// TestApplySchemeIdempotent covers repeated application: nothing may drift on
// a second call with the same scheme.
func TestApplySchemeIdempotent(t *testing.T) {
	defer applyScheme(defaultScheme)

	for _, name := range []string{defaultScheme, testnetScheme} {
		applyScheme(name)
		first := colouredStyles()
		applyScheme(name)
		second := colouredStyles()

		for k, v := range first {
			if second[k] != v {
				t.Errorf("scheme %q: %s drifted on second call: %v -> %v", name, k, v, second[k])
			}
		}
	}
}

// TestSchemeRoundTrip covers the config modal toggling the network off again:
// the original look has to come back, not a half-orange hybrid.
func TestSchemeRoundTrip(t *testing.T) {
	defer applyScheme(defaultScheme)

	applyScheme(defaultScheme)
	want := colouredStyles()

	applyScheme(testnetScheme)
	applyScheme(defaultScheme)
	got := colouredStyles()

	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s after round-trip = %v, want %v", k, got[k], v)
		}
	}
}

// TestUnknownSchemeFallsBackToDefault keeps a typo'd or removed scheme name
// from rendering an all-black UI built from zero-value colours.
func TestUnknownSchemeFallsBackToDefault(t *testing.T) {
	defer applyScheme(defaultScheme)

	applyScheme(defaultScheme)
	want := colouredStyles()

	applyScheme("no-such-scheme")
	got := colouredStyles()

	for k, v := range want {
		if got[k] != v {
			t.Errorf("unknown scheme: %s = %v, want default's %v", k, got[k], v)
		}
	}
}

// TestApplyNetworkPaletteSelectsScheme pins the network -> scheme mapping the
// two call sites in main.go and update.go rely on.
func TestApplyNetworkPaletteSelectsScheme(t *testing.T) {
	defer applyScheme(defaultScheme)

	applyNetworkPalette(true)
	gotTestnet := colouredStyles()
	applyScheme(testnetScheme)
	wantTestnet := colouredStyles()
	for k, v := range wantTestnet {
		if gotTestnet[k] != v {
			t.Fatalf("applyNetworkPalette(true) did not select %q: %s = %v, want %v",
				testnetScheme, k, gotTestnet[k], v)
		}
	}

	applyNetworkPalette(false)
	gotMainnet := colouredStyles()
	applyScheme(defaultScheme)
	wantMainnet := colouredStyles()
	for k, v := range wantMainnet {
		if gotMainnet[k] != v {
			t.Fatalf("applyNetworkPalette(false) did not select %q: %s = %v, want %v",
				defaultScheme, k, gotMainnet[k], v)
		}
	}
}

// TestSchemesAreComplete catches a new scheme that forgets a field: a
// zero-value lipgloss.Color renders as unset, which reads as an invisible or
// default-coloured element rather than an obvious error.
func TestSchemesAreComplete(t *testing.T) {
	for name, p := range schemes {
		fields := map[string]lipgloss.Color{
			"border": p.border, "muted": p.muted, "label": p.label,
			"value": p.value, "title": p.title, "accent": p.accent,
			"rowSelected": p.rowSelected, "good": p.good, "warn": p.warn,
			"bad": p.bad, "mainnet": p.mainnet, "testnet": p.testnet,
		}
		for field, c := range fields {
			if c == "" {
				t.Errorf("scheme %q: field %s is unset", name, field)
			}
		}
	}
}
