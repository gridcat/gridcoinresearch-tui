package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// pollSettleDelay is how long the cursor must rest on a poll before its tally is
// fetched. Short enough to feel instant when you stop, long enough to skip
// polls you scroll straight through.
const pollSettleDelay = 350 * time.Millisecond

// fetchPolls loads the governance poll list. includeFinished maps to
// listpolls's showfinished argument. Fired only when the polls screen is
// opened / refreshed / toggled, never on the refresh tick.
func fetchPolls(c *rpc.Client, includeFinished bool) tea.Cmd {
	return func() tea.Msg {
		polls, err := c.ListPolls(includeFinished)
		return pollsMsg{includeFinished: includeFinished, polls: polls, err: err}
	}
}

// fetchPollResult runs the getpollresults tally for a single poll. Heavy, so
// it's fired lazily for just the poll under the cursor (see ensurePollResult).
func fetchPollResult(c *rpc.Client, id string) tea.Cmd {
	return func() tea.Msg {
		r, err := c.GetPollResults(id)
		return pollResultMsg{id: id, result: r, err: err}
	}
}

// reloadPolls (re)loads the poll list for the current all/active scope and
// bumps the inflight spinner. Called on open, on "r", and after a tab toggle.
// It also clears the cached getpollresults tallies; without that,
// ensurePollResult's cache guard would keep serving each poll's first tally and
// newer votes would stay hidden until restart.
func (m *Model) reloadPolls() tea.Cmd {
	m.pollResults = make(map[string]rpc.PollResult)
	m.pollResultPending = make(map[string]bool)
	m.pollResultErr = make(map[string]string)
	spin := m.bumpInflight(1)
	return tea.Batch(fetchPolls(m.rpc, m.pollsShowFinished), spin)
}

// ensurePollResult lazily kicks off the getpollresults tally for the poll
// under the cursor, unless it's already cached or a fetch is already in
// flight. Returns nil (a no-op Cmd for tea.Batch) when there's nothing to do,
// so cursor-move handlers can call it unconditionally.
func (m *Model) ensurePollResult() tea.Cmd {
	p := m.selectedPoll()
	if p == nil || p.ID == "" {
		return nil
	}
	if _, cached := m.pollResults[p.ID]; cached {
		return nil
	}
	if m.pollResultPending[p.ID] {
		return nil
	}
	m.pollResultPending[p.ID] = true
	spin := m.bumpInflight(1)
	return tea.Batch(fetchPollResult(m.rpc, p.ID), spin)
}

// retryPollResult clears a failed tally's error for the selected poll and asks
// ensurePollResult to re-issue it, the detail popup's "r" key. A tally
// already cached or in flight is left alone.
func (m *Model) retryPollResult() tea.Cmd {
	if p := m.selectedPoll(); p != nil {
		delete(m.pollResultErr, p.ID)
	}
	return m.ensurePollResult()
}

// schedulePollSettle arms the debounce timer for the currently-selected poll.
// Used on cursor moves and after the list loads: only once the cursor has
// rested on a poll for pollSettleDelay does its (heavy) tally actually fire, so
// scrolling through the list no longer kicks off a getpollresults per row.
// Opening the detail popup with enter still fetches immediately.
func (m *Model) schedulePollSettle() tea.Cmd {
	p := m.selectedPoll()
	if p == nil || p.ID == "" {
		return nil
	}
	id := p.ID
	return tea.Tick(pollSettleDelay, func(time.Time) tea.Msg {
		return pollSettleMsg{id: id}
	})
}

// handlePollsKey drives the full-screen polls list. Navigation is
// self-contained (it moves m.pollCursor directly rather than going through
// focusedList, which only knows the dashboard's tx/address panels). Every
// cursor move returns ensurePollResult() so the lazy tally for the newly
// selected poll starts fetching.
func (m Model) handlePollsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Cases that don't just move the cursor return early. The remaining
	// movement keys share one clamp + ensurePollResult tail, so they only set a
	// target and fall through. clampCursor pins the target to [0, len-1], so
	// g/G (0 and len-1) fold in without special-casing.
	target := m.pollCursor
	switch msg.String() {
	case "esc", "q":
		m.mode = modeDashboard
		return m, nil
	case "r":
		return m, m.reloadPolls()
	case "tab":
		// Toggle between all polls (incl. finished) and active only, then
		// reload. Reset the cursor since the row set changes.
		m.pollsShowFinished = !m.pollsShowFinished
		m.pollCursor = 0
		return m, m.reloadPolls()
	case "enter":
		// Open the detail popup for the selected poll. Its tally was usually
		// fetched already when the cursor landed here, but ensurePollResult also
		// (re)starts it if the list prefetch hasn't run or a prior attempt
		// errored. It is a no-op when the tally is cached or already in flight.
		if m.selectedPoll() == nil {
			return m, nil
		}
		m.mode = modePollDetail
		return m, m.ensurePollResult()
	case "up", "k":
		target = m.pollCursor - 1
	case "down", "j":
		target = m.pollCursor + 1
	case "pgup", "ctrl+u":
		target = m.pollCursor - pageSize
	case "pgdown", "ctrl+d":
		target = m.pollCursor + pageSize
	case "g", "home":
		target = 0
	case "G", "end":
		target = len(m.polls) - 1
	default:
		return m, nil
	}
	m.pollCursor = clampCursor(target, len(m.polls))
	return m, m.schedulePollSettle()
}

// renderPollsScreen is the full-screen governance polls list (mode "p"). It
// reuses the dashboard header (network badge + block height) so the chrome
// matches, fills the middle with the scrollable poll list, and pins a
// polls-specific key legend at the bottom.
func (m Model) renderPollsScreen() string {
	header := m.renderHeader()
	footer := m.renderPollsFooter()
	available := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if available < 3 {
		available = 3
	}
	body := m.renderPollsList(available)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// renderPollsList draws the scrollable poll rows. Same cursor-window scroll
// math as renderTxList: the offset is derived fresh each frame from pollCursor
// so nothing needs storing on the by-value Model.
func (m Model) renderPollsList(height int) string {
	boxStyle := theme.BorderFocused.Width(m.width - 2).Height(height - 2)
	scope := "all"
	if !m.pollsShowFinished {
		scope = "active"
	}
	// The polls screen is always the focused surface, so its title takes the
	// accent the way a focused dashboard panel's does.
	box := func(content string) string {
		return ui.TitledBox(boxStyle, theme.Accent, "Polls · "+scope, content)
	}

	// Loading / error / empty each render as a single status line.
	var status string
	switch {
	case !m.pollsLoaded:
		status = theme.Muted.Render("loading…")
	case m.pollsErr != "":
		status = theme.Bad.Render("error: " + format.SanitizeTerminal(m.pollsErr))
	case len(m.polls) == 0:
		status = theme.Muted.Render("no polls")
	}
	if status != "" {
		return box(status)
	}

	maxRows, offset := ui.ListWindow(height, m.pollCursor, len(m.polls))
	var lines []string
	for i := offset; i < offset+maxRows && i < len(m.polls); i++ {
		prefix := "  "
		line := m.renderPollRow(m.polls[i])
		if i == m.pollCursor {
			prefix = theme.Accent.Background(theme.ColorRowSelected).Render("▸ ")
			line = ui.FillBackground(line, m.panelRowWidth())
		}
		lines = append(lines, prefix+line)
	}
	return box(strings.Join(lines, "\n"))
}

// renderPollRow renders one poll line: status dot · title · weight-type ·
// stat · time-left. The stat column shows the lazily-fetched tally
// ("62% Yes") once it's cached, otherwise the cheap "N votes" count from
// listpolls.
func (m Model) renderPollRow(p rpc.Poll) string {
	// Parse the expiration once and derive both the status dot and the
	// time-left column from it (both PollExpired and FormatPollTimeLeft would
	// otherwise re-parse the same string every frame).
	exp := format.ParsePollTime(p.Expiration)
	dot := theme.Good.Render("●")
	if format.PollExpiredAt(exp) {
		dot = theme.Muted.Render("○")
	}

	// Flex the title to fill the row: panel width minus the dot (1), its
	// trailing space (1), the two-space gap before the stat column, and the
	// three fixed columns. GetWidth keeps this correct if those widths change.
	fixed := 2 + theme.PollWeightCol.GetWidth() + 2 + theme.PollStatCol.GetWidth() + theme.PollTimeCol.GetWidth()
	titleWidth := m.panelRowWidth() - fixed
	if titleWidth < 12 {
		titleWidth = 12
	}
	// Poll title, weight type and leading choice are on-chain data any network
	// participant can author (see sanitizeTerminal), cleaned before truncate /
	// ShortWeightType so the width budget matches the printed text.
	title := lipgloss.NewStyle().Width(titleWidth).Render(ui.Truncate(format.SanitizeTerminal(p.Title), titleWidth-1))
	weight := theme.PollWeightCol.Render(format.ShortWeightType(format.SanitizeTerminal(p.WeightType)))

	var stat string
	if r, ok := m.pollResults[p.ID]; ok {
		pct := "—"
		if r.VotePercentAVW != nil {
			pct = fmt.Sprintf("%.0f%%", *r.VotePercentAVW)
		}
		leader := format.SanitizeTerminal(r.TopChoice)
		if leader == "" {
			leader = "—"
		}
		stat = fmt.Sprintf("%-4s %s", pct, leader)
	} else {
		stat = fmt.Sprintf("%d votes", p.Votes)
	}
	statCol := theme.PollStatCol.Render(ui.Truncate(stat, 21))

	timeCol := theme.PollTimeCol.Render(format.PollTimeLeftAt(exp))

	return lipgloss.JoinHorizontal(lipgloss.Top, dot, " ", title, weight, "  ", statCol, timeCol)
}

// renderPollDetailModal is the centered popup opened with enter on a selected
// poll. It shows the full poll metadata plus, from the lazily-fetched
// getpollresults tally cached in m.pollResults, a per-choice results breakdown
// (each option's share of the total weight as a bar). If the tally is still in
// flight the results section shows "tallying…" and fills in when it lands.
func (m Model) renderPollDetailModal() string {
	p := m.selectedPoll()
	if p == nil {
		// Selection vanished (list reloaded to empty); fall back to the list.
		return m.renderPollsScreen()
	}

	field := func(label, value string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			theme.Label.Width(14).Render(label),
			theme.Value.Render(value),
		)
	}
	orDash := func(s string) string {
		if s == "" {
			return "—"
		}
		return s
	}

	exp := format.ParsePollTime(p.Expiration)
	var status string
	if format.PollExpiredAt(exp) {
		status = theme.Muted.Render("○ ended")
	} else {
		status = theme.Good.Render("● active") + theme.Muted.Render(" · "+format.PollTimeLeftAt(exp)+" left")
	}

	// The daemon-sourced values are sanitized at the call sites, not inside
	// field: field also receives values we already rendered ourselves (the
	// Status line above, and its counterpart in renderTxDetailModal), and
	// sanitizing those would strip our own SGR colour escapes along with the
	// hostile ones. Everything here is on-chain poll-author data, cleaned
	// before truncate so the column budget matches the printed text.
	lines := []string{
		field("Title", format.SanitizeTerminal(p.Title)),
		field("Status", status),
		field("Question", orDash(ui.Truncate(format.SanitizeTerminal(p.Question), 74))),
		field("URL", orDash(ui.Truncate(format.SanitizeTerminal(p.URL), 74))),
		field("Weight type", orDash(format.SanitizeTerminal(p.WeightType))),
		field("Responses", orDash(format.SanitizeTerminal(p.ResponseType))),
		field("Created", orDash(format.SanitizeTerminal(p.Timestamp))),
		field("Duration", fmt.Sprintf("%d days", p.DurationDays)),
		field("Votes", fmt.Sprintf("%d", p.Votes)),
	}

	r, ok := m.pollResults[p.ID]
	if ok && r.VotePercentAVW != nil {
		lines = append(lines, field("Participation", fmt.Sprintf("%.1f%% AVW", *r.VotePercentAVW)))
	}

	lines = append(lines, "", theme.Title.Render("Results"))
	switch {
	case ok && len(r.Responses) == 0:
		lines = append(lines, theme.Muted.Render("  no votes yet"))
	case !ok && m.pollResultErr[p.ID] != "":
		// The tally finished with an error (e.g. a transient reorg per the
		// daemon's getpollresults note). Show it instead of a stuck spinner.
		lines = append(lines,
			theme.Bad.Render("  couldn't load results: "+format.SanitizeTerminal(m.pollResultErr[p.ID])),
			theme.Muted.Render("  press r to retry"))
	case !ok:
		// Pending, or the brief window just after opening: animate so it's
		// clearly still working, not frozen.
		lines = append(lines, theme.Muted.Render("  tallying… "+ui.SpinnerFrames[m.spinnerFrame]))
	default:
		// Each response is two lines: the full choice text (poll answers are
		// often whole sentences, so truncating them into a column would hide the
		// point), then an indented stats line: a share bar plus labelled
		// numbers, so it's clear what each figure means without a column header.
		for _, resp := range r.Responses {
			frac := 0.0
			if r.TotalWeight > 0 {
				frac = resp.Weight / r.TotalWeight
			}
			stats := fmt.Sprintf("%.0f%% share · %s weight · %s votes",
				frac*100, format.FormatCompactNumber(resp.Weight), format.FormatVoteCount(resp.Votes))
			lines = append(lines,
				"  "+theme.Value.Render(format.SanitizeTerminal(resp.Choice)),
				lipgloss.JoinHorizontal(lipgloss.Top, "    ", ui.Bar(frac, 16), "  ", theme.Muted.Render(stats)),
			)
		}
	}
	lines = append(lines, "", theme.Muted.Render("enter/esc to close"))

	width := m.width - 8
	if width > 96 {
		width = 96
	}
	if width < 40 {
		width = 40
	}
	modal := ui.ModalBox(width, "Poll", strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// onPollsMsg handles pollsMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onPollsMsg(msg pollsMsg) (tea.Model, tea.Cmd) {
	m.finishFetch()
	// Drop a reply whose scope no longer matches what the screen is now
	// showing (the user toggled all/active while this was in flight). A
	// newer request for the current scope is the authoritative one.
	if msg.includeFinished != m.pollsShowFinished {
		return m, nil
	}
	if msg.err != nil {
		m.pollsErr = msg.err.Error()
		m.pollsLoaded = true
		return m, nil
	}
	m.polls = msg.polls
	// Order newest-first by posting date so the most recent polls are at
	// the top. Unparseable timestamps sort as the zero time and sink to the
	// bottom. SliceStable keeps the daemon's order among equal timestamps.
	sort.SliceStable(m.polls, func(i, j int) bool {
		return format.ParsePollTime(m.polls[i].Timestamp).After(format.ParsePollTime(m.polls[j].Timestamp))
	})
	m.pollsLoaded = true
	m.pollsErr = ""
	m.pollCursor = clampCursor(m.pollCursor, len(m.polls))
	// Debounce the tally for whichever poll the cursor now rests on, so a
	// quick reload+scroll doesn't fetch one you're about to leave.
	return m, m.schedulePollSettle()
}

// onPollSettleMsg handles pollSettleMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onPollSettleMsg(msg pollSettleMsg) (tea.Model, tea.Cmd) {
	// The debounce timer fired: only fetch if the cursor is still on the
	// poll that armed it. Otherwise the user moved on and a later timer
	// (armed by that move) will handle the new selection.
	if p := m.selectedPoll(); p != nil && p.ID == msg.id {
		return m, m.ensurePollResult()
	}
	return m, nil
}

// onPollResultMsg handles pollResultMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onPollResultMsg(msg pollResultMsg) (tea.Model, tea.Cmd) {
	m.finishFetch()
	delete(m.pollResultPending, msg.id)
	if msg.err != nil {
		// Record the error so the detail popup can show why the tally
		// failed (rather than a perpetual "tallying…") and offer a retry.
		// The list row still falls back to the "N votes" count.
		m.pollResultErr[msg.id] = msg.err.Error()
		return m, nil
	}
	delete(m.pollResultErr, msg.id)
	m.pollResults[msg.id] = msg.result
	return m, nil
}

// handlePollDetailKey drives the poll detail popup: it closes back to the
// list, or retries a failed tally without leaving the popup.
func (m Model) handlePollDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "enter":
		m.mode = modePolls
		return m, nil
	case "r":
		return m, m.retryPollResult()
	}
	return m, nil
}

// selectedPoll returns the poll the cursor is on in the polls screen, or nil
// when the list is empty or the cursor is out of range.
func (m Model) selectedPoll() *rpc.Poll {
	if m.pollCursor < 0 || m.pollCursor >= len(m.polls) {
		return nil
	}
	return &m.polls[m.pollCursor]
}
