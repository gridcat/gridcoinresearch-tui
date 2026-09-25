package theme

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
)

// Colour scheme. lipgloss.Color accepts any 256-colour terminal code as a
// decimal string, and the terminal renders it via ANSI SGR. Where the
// terminal doesn't support colour, lipgloss strips the escape sequences.
//
// A scheme is a Palette value plus an entry in the Schemes map below. Nothing
// else needs touching to add one: BuildStyles consumes the Palette generically,
// so every style picks the new colours up automatically.
type Palette struct {
	border      lipgloss.Color
	muted       lipgloss.Color
	label       lipgloss.Color
	value       lipgloss.Color
	title       lipgloss.Color
	accent      lipgloss.Color
	rowSelected lipgloss.Color // highlight background for the selected row

	// Status colours. A scheme is free to restyle these to fit its Palette,
	// but the three must stay clearly distinguishable from each other and from
	// the chrome: they are the only cue for state rather than decoration, so
	// "staking ● yes" must never be mistakable for an error.
	good lipgloss.Color
	warn lipgloss.Color
	bad  lipgloss.Color

	// Network badge colours, used by the header's "● mainnet" / "● testnet".
	mainnet lipgloss.Color
	testnet lipgloss.Color
}

// Schemes holds every selectable colour scheme. Add a scheme by adding an
// entry here and pointing something at its name.
var Schemes = map[string]Palette{
	// "default" is the original neutral look: grey chrome, blue accent.
	"default": {
		border:      lipgloss.Color("240"),
		muted:       lipgloss.Color("244"),
		label:       lipgloss.Color("250"),
		value:       lipgloss.Color("255"),
		title:       lipgloss.Color("255"),
		accent:      lipgloss.Color("75"), // blue
		rowSelected: lipgloss.Color("236"),
		good:        lipgloss.Color("42"),  // green
		warn:        lipgloss.Color("214"), // orange
		bad:         lipgloss.Color("203"), // red
		mainnet:     lipgloss.Color("42"),
		testnet:     lipgloss.Color("214"),
	},

	// "orange" is the testnet look, matching the orange-for-testnet convention
	// the *.gridcoin.club frontends use so a testnet window is unmistakable
	// among mainnet ones. The warm neutrals form a deliberate ramp (muted 137
	// < border 172 < label 180 < accent 208 < title 214 < value 230), so text
	// hierarchy survives even though nearly everything is orange.
	//
	// The status colours are warmed too rather than left green/red, so nothing
	// on screen breaks the theme. They stay mutually distinct by hue instead of
	// by temperature: yellow 184 (good) / orange 214 (warn) / red-orange 202
	// (bad) still reads as a traffic light, just a warm one.
	"orange": {
		border:      lipgloss.Color("172"),
		muted:       lipgloss.Color("137"),
		label:       lipgloss.Color("180"),
		value:       lipgloss.Color("230"),
		title:       lipgloss.Color("214"), // ~ family testnet primary #ef6c00
		accent:      lipgloss.Color("208"),
		rowSelected: lipgloss.Color("58"),
		good:        lipgloss.Color("184"), // yellow
		warn:        lipgloss.Color("214"), // orange
		bad:         lipgloss.Color("202"), // red-orange
		mainnet:     lipgloss.Color("184"),
		testnet:     lipgloss.Color("214"),
	},
}

// Live colours, assigned by ApplyPalette. A few render paths read these
// directly rather than through a style (BorderForeground on modal boxes, the
// selected-row background), so they have to stay in sync with the styles.
var (
	ColorBorder      lipgloss.Color
	ColorMuted       lipgloss.Color
	ColorLabel       lipgloss.Color
	ColorValue       lipgloss.Color
	ColorGood        lipgloss.Color
	ColorWarn        lipgloss.Color
	ColorBad         lipgloss.Color
	ColorMainnet     lipgloss.Color
	ColorTestnet     lipgloss.Color
	ColorAccent      lipgloss.Color
	ColorRowSelected lipgloss.Color
)

// Styles built from the live colours. These are assigned by BuildStyles, NOT
// at declaration: a style captures its colour by value, so one built at
// declaration time would keep the first scheme's colours forever.
//
// Anything here that captures a colour must be (re)built in BuildStyles.
// Colour-free styles live in the plain var block further down.
var (
	// Border is the rounded-corner box used for every panel on the
	// dashboard. Padding(0, 1) inserts one column of horizontal breathing
	// room inside the border on each side.
	Border lipgloss.Style

	// BorderFocused is the same rounded box but painted with the
	// accent colour so the user can tell at a glance which panel arrow
	// keys will operate on.
	BorderFocused lipgloss.Style

	Label  lipgloss.Style
	Value  lipgloss.Style
	Muted  lipgloss.Style
	Good   lipgloss.Style
	Warn   lipgloss.Style
	Bad    lipgloss.Style
	Accent lipgloss.Style
	Title  lipgloss.Style

	MainnetBadge lipgloss.Style
	TestnetBadge lipgloss.Style

	StatLabelA lipgloss.Style
	StatLabelB lipgloss.Style

	TxStatusCol lipgloss.Style

	// Poll list columns. Title has no fixed width; it flexes to fill whatever
	// these three fixed columns leave (see renderPollRow), so the row spans the
	// full damn panel and the title gets the most room. The stat column holds
	// either the cheap "N votes" count or, once the lazy tally lands, the "62% Yes"
	// participation + leading answer.
	PollWeightCol lipgloss.Style
	PollStatCol   lipgloss.Style
	PollTimeCol   lipgloss.Style

	// TxKindStyle maps the status enum defined in format.go to the lipgloss
	// colour we want its icon rendered in. Package-level map so renderTxRow
	// doesn't build one on each frame.
	TxKindStyle map[format.TxStatusKind]lipgloss.Style

	ConfigLabel        lipgloss.Style
	ConfigLabelFocused lipgloss.Style
	ConfigValueFocused lipgloss.Style
)

// Styles with no colour of their own: layout only, so they are scheme
// independent and safe to build once at declaration.
var (
	StatValueA = lipgloss.NewStyle().Width(22)

	TxAmountCol = lipgloss.NewStyle().Width(18).Align(lipgloss.Right)
	TxAddrCol   = lipgloss.NewStyle().Width(16)
	TxTimeCol   = lipgloss.NewStyle().Width(12)
)

// Status glyphs for the compact dashboard, where icons replace words. They
// are single-width symbols, never emoji, because terminals disagree on how
// wide an emoji is. Keeping them together here means a limited character set
// can swap them all for plain letters in one place.
const (
	GlyphOn       = "●" // staking, network badge
	GlyphOff      = "○" // not staking, unencrypted
	GlyphLocked   = "■"
	GlyphUnlocked = "◐"
	GlyphCruncher = "★"
	GlyphInvestor = "☆"
	GlyphPeers    = "⇅"
	GlyphUpdate   = "↑"
	GlyphError    = "✗"
	GlyphTab      = "⇥"
)

func init() { ApplyScheme(DefaultScheme) }

const (
	DefaultScheme = "default"
	TestnetScheme = "orange"
)

// ApplyScheme repaints everything from the named scheme, falling back to the
// default if the name is unknown so a bad name degrades to a plain UI instead
// of a blank one.
func ApplyScheme(name string) {
	p, ok := Schemes[name]
	if !ok {
		p = Schemes[DefaultScheme]
	}
	ApplyPalette(p)
}

// ApplyNetwork selects the scheme for the network we're pointed at.
// Colour is what makes a testnet window recognisable at a glance among
// mainnet ones in a row of tmux panes.
func ApplyNetwork(testnet bool) {
	if testnet {
		ApplyScheme(TestnetScheme)
		return
	}
	ApplyScheme(DefaultScheme)
}

// ApplyPalette publishes a Palette to the live colours and rebuilds every
// style from them. Safe to call repeatedly and in any order, since it assigns all
// state unconditionally rather than mutating in place, so the config modal can
// toggle Schemes at runtime without a restart.
func ApplyPalette(p Palette) {
	ColorBorder = p.border
	ColorMuted = p.muted
	ColorLabel = p.label
	ColorValue = p.value
	ColorAccent = p.accent
	ColorRowSelected = p.rowSelected
	ColorGood = p.good
	ColorWarn = p.warn
	ColorBad = p.bad
	ColorMainnet = p.mainnet
	ColorTestnet = p.testnet
	BuildStyles(p)
}

// BuildStyles rebuilds every style that captures a colour. Adding a coloured
// style means adding it here too, otherwise it silently keeps whichever
// scheme happened to be active when it was first built.
func BuildStyles(p Palette) {
	Border = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(0, 1)
	BorderFocused = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.accent).
		Padding(0, 1)

	Label = lipgloss.NewStyle().Foreground(p.label)
	Value = lipgloss.NewStyle().Foreground(p.value).Bold(true)
	Muted = lipgloss.NewStyle().Foreground(p.muted)
	Good = lipgloss.NewStyle().Foreground(p.good)
	Warn = lipgloss.NewStyle().Foreground(p.warn)
	Bad = lipgloss.NewStyle().Foreground(p.bad)
	Accent = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	Title = lipgloss.NewStyle().Foreground(p.title).Bold(true)

	MainnetBadge = lipgloss.NewStyle().Foreground(p.mainnet).Bold(true)
	TestnetBadge = lipgloss.NewStyle().Foreground(p.testnet).Bold(true)

	// 15 so the longest labels ("Immature Stake", "Pending Reward", both 14
	// chars) keep a separating space before the value column.
	StatLabelA = Label.Width(15)
	StatLabelB = Label.Width(12)

	TxStatusCol = lipgloss.NewStyle().Width(10).Foreground(p.label)

	PollWeightCol = lipgloss.NewStyle().Width(6).Foreground(p.muted)
	PollStatCol = lipgloss.NewStyle().Width(22).Foreground(p.muted)
	PollTimeCol = lipgloss.NewStyle().Width(8).Align(lipgloss.Right).Foreground(p.muted)

	TxKindStyle = map[format.TxStatusKind]lipgloss.Style{
		format.TxStatusUpcoming:  Warn,
		format.TxStatusIncoming:  Accent,
		format.TxStatusSending:   Accent,
		format.TxStatusConfirmed: Good,
		format.TxStatusStake:     Accent,
	}

	ConfigLabel = Label.Width(12)
	ConfigLabelFocused = Accent.Width(12)
	ConfigValueFocused = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
}
