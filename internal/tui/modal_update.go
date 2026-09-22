package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/buildinfo"
	"github.com/gridcat/gridcoinresearch-tui/internal/selfupdate"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// checkUpdateCmd queries GitHub for the latest release. It runs on a goroutine
// like every other Cmd; the short HTTP timeout in fetchLatestRelease keeps a
// blocked network from wedging the loop. Note it does NOT touch m.inflight:
// the update flow is intentionally separate from the RPC refresh spinner so a
// silent background check never makes the dashboard footer flash "refreshing".
func checkUpdateCmd(manual bool) tea.Cmd {
	return func() tea.Msg {
		rel, err := selfupdate.FetchLatest(selfupdate.APIBase)
		if err != nil {
			return updateCheckMsg{err: err, manual: manual}
		}
		var releases []selfupdate.Release
		if manual {
			releases, err = selfupdate.FetchReleases(selfupdate.APIBase)
			if err != nil {
				return updateCheckMsg{err: err, manual: manual}
			}
		}
		return updateCheckMsg{rel: rel, missedReleases: selfupdate.Missed(buildinfo.Version, rel, releases), manual: manual}
	}
}

// runUpdate downloads, verifies and swaps the binary for the given release.
func runUpdate(rel selfupdate.Release) tea.Cmd {
	return func() tea.Msg {
		newExe, err := selfupdate.Apply(rel)
		return updateInstallMsg{newExe: newExe, err: err}
	}
}

// updateTickCmd schedules the next background update check.
func (m *Model) updateTickCmd() tea.Cmd {
	return tea.Tick(selfupdate.CheckInterval, func(t time.Time) tea.Msg { return updateTickMsg(t) })
}

// ---- Init / Update ----------------------------------------------------

// handleUpdateKey drives the self-update modal. The current step decides which
// keys do what: while a check or install is in flight most keys are inert so a
// stray keystroke can't interrupt it.
func (m Model) handleUpdateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.update.step {
	case updateStepInstalling:
		// Don't let any keystroke interrupt an in-flight download/swap.
		return m, nil
	case updateStepAvailable:
		switch msg.String() {
		case "y", "enter":
			m.update.step = updateStepInstalling
			m.update.errMsg = ""
			return m, runUpdate(m.update.rel)
		case "n", "esc", "q":
			m.mode = modeDashboard
		}
		return m, nil
	default:
		// checking / upToDate / failed: esc/q/enter closes; other keys ignored.
		// Closing during a check is fine; the goroutine still finishes and
		// updates the badge; it just no longer has a modal to advance.
		switch msg.String() {
		case "esc", "q", "enter":
			m.mode = modeDashboard
		}
		return m, nil
	}
}

// displayVersion is the build version for the UI, with "dev" spelled out so a
// local build reads clearly rather than showing a bare "dev".
func displayVersion() string {
	if buildinfo.Version == "dev" {
		return "dev build"
	}
	return "v" + buildinfo.Version
}

// clampChangelog trims an over-long changelog so the modal keeps a sane height
// and the confirm keys never get pushed off screen. A single version's notes
// are normally a few lines; this only bites on an unusually chatty release.
func clampChangelog(s string) string {
	const maxLines = 12
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], theme.Muted.Render("… (see the release page for the rest)"))
	}
	return strings.Join(lines, "\n")
}

func renderReleaseChangelogs(releases []selfupdate.Release) string {
	if len(releases) == 0 {
		return theme.Muted.Render("(no release notes)")
	}
	sections := make([]string, 0, len(releases))
	for _, rel := range releases {
		notes := selfupdate.TrimStampSection(rel.Body)
		if notes == "" {
			notes = theme.Muted.Render("(no release notes)")
		}
		version := "v" + strings.TrimPrefix(rel.TagName, "v")
		sections = append(sections, theme.Title.Render(version)+"\n"+notes)
	}
	return clampChangelog(strings.Join(sections, "\n\n"))
}

// renderUpdateModal draws the self-update flow: a live check, then either
// "up to date" or a changelog + confirm, then an install progress line. The
// changelog is the original release notes with the on-chain stamp section
// stripped (see trimStampSection).
func (m Model) renderUpdateModal() string {
	var body string
	switch m.update.step {
	case updateStepChecking:
		body = theme.Muted.Render("Checking GitHub for a newer release…")
	case updateStepUpToDate:
		body = theme.Good.Render("✓ You're on the latest version") + "\n\n" +
			theme.Muted.Render("Current: "+displayVersion()) + "\n\n" +
			theme.Muted.Render("[esc] close")
	case updateStepAvailable:
		latest := "v" + strings.TrimPrefix(m.update.rel.TagName, "v")
		body = "Current " + displayVersion() + "  →  " + theme.Good.Render(latest) + "\n\n"
		body += theme.Title.Render("What changed") + "\n" + renderReleaseChangelogs(m.update.missedReleases) + "\n\n"
		body += theme.Warn.Render("Downloads and replaces the binary, then restarts the TUI.") + "\n\n"
		body += theme.Muted.Render("[y] Update & restart   [n] Cancel")
	case updateStepInstalling:
		body = theme.Muted.Render("Downloading and installing…") + "\n\n" +
			theme.Muted.Render("The TUI will restart automatically when it's done.")
	case updateStepFailed:
		body = theme.Bad.Render("Update failed") + "\n\n" + m.update.errMsg + "\n\n" +
			theme.Muted.Render("[esc] close")
	}

	modalWidth := 64
	if max := m.width - 4; modalWidth > max && max > 0 {
		modalWidth = max
	}
	modal := ui.ModalBox(modalWidth, "Updates", body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// onUpdateTickMsg handles updateTickMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onUpdateTickMsg(msg updateTickMsg) (tea.Model, tea.Cmd) {
	// Long-interval background re-check. Re-arm the timer and fire another
	// silent check (respecting a late opt-out, though Init won't arm this
	// when NoUpdateCheck is set).
	if m.cfg.NoUpdateCheck {
		return m, nil
	}
	return m, tea.Batch(m.updateTickCmd(), checkUpdateCmd(false))
}

// onUpdateCheckMsg handles updateCheckMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onUpdateCheckMsg(msg updateCheckMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// Only a manual check may surface a failure in the modal. A racing
		// background error must never flip a manual check the user is
		// waiting on to "failed", because the manual success would then be
		// unable to recover the modal (its advance only fires from "checking").
		if msg.manual && m.mode == modeUpdate && m.update.step == updateStepChecking {
			m.update.step = updateStepFailed
			m.update.errMsg = msg.err.Error()
		}
		return m, nil
	}
	// Any successful check, manual or background, refreshes the badge.
	// Only a manual check owns the modal payload; background checks do not
	// fetch missed release notes and must not collapse an open changelog to
	// latest-only while the user is reading it.
	m.latestVersion = strings.TrimPrefix(msg.rel.TagName, "v")
	m.updateAvailable = selfupdate.IsNewer(msg.rel.TagName, buildinfo.Version)
	if msg.manual {
		m.update.rel = msg.rel
		m.update.missedReleases = msg.missedReleases
		if len(m.update.missedReleases) == 0 {
			m.update.missedReleases = selfupdate.Missed(buildinfo.Version, msg.rel, nil)
		}
	}
	// Only a manual check drives the modal, and only from its "checking"
	// state. That ignores a background check landing while the user reads
	// the changelog or is mid-install, so it can't yank the view around.
	if msg.manual && m.mode == modeUpdate && m.update.step == updateStepChecking {
		if m.updateAvailable || buildinfo.Version == "dev" {
			m.update.step = updateStepAvailable
		} else {
			m.update.step = updateStepUpToDate
		}
	}
	return m, nil
}

// onUpdateInstallMsg handles updateInstallMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onUpdateInstallMsg(msg updateInstallMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.update.step = updateStepFailed
		m.update.errMsg = msg.err.Error()
		return m, nil
	}
	// Binary swapped. Record the path and quit; main.go re-execs it after
	// Bubble Tea restores the terminal.
	m.restartExe = msg.newExe
	return m, tea.Quit
}

// updateStep is the state-machine step inside the self-update modal.
type updateStep int

const (
	updateStepChecking   updateStep = iota // querying GitHub for the latest release
	updateStepUpToDate                     // already on the latest release
	updateStepAvailable                    // a newer release exists; awaiting confirm
	updateStepInstalling                   // downloading + verifying + swapping the binary
	updateStepFailed                       // the check or install errored
)

// updateState is the live state of the self-update modal. The release payload
// is cached from the check so the confirm/install step doesn't re-fetch it.
type updateState struct {
	step           updateStep
	rel            selfupdate.Release   // the latest release from the most recent check
	missedReleases []selfupdate.Release // releases newer than the running version, used for changelog display
	errMsg         string               // populated in updateStepFailed
}
