package theme

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// ConfigureColorProfile works around termenv v0.15.2 classifying a
// plain Unix TERM=xterm as Ascii. Plain xterm guarantees the standard ANSI
// Palette, but not 256 colors, so the fallback deliberately stops at ANSI.
func ConfigureColorProfile() {
	detected := lipgloss.ColorProfile()
	profile := colorProfileWithXtermFallback(
		detected,
		term.IsTerminal(os.Stdout.Fd()),
		termenv.EnvNoColor(),
		os.Getenv("CI"),
		os.Getenv("TERM"),
	)
	if profile != detected {
		lipgloss.SetColorProfile(profile)
	}
}

// colorProfileWithXtermFallback contains the policy separately from process
// globals so all capability and opt-out combinations can be unit-tested.
func colorProfileWithXtermFallback(
	detected termenv.Profile,
	stdoutIsTTY bool,
	colorDisabled bool,
	ci string,
	termName string,
) termenv.Profile {
	if detected == termenv.Ascii &&
		stdoutIsTTY &&
		!colorDisabled &&
		ci == "" &&
		strings.EqualFold(termName, "xterm") {
		return termenv.ANSI
	}

	return detected
}
