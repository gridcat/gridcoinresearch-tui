package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/state"
	"github.com/gridcat/gridcoinresearch-tui/internal/telemetry"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// sharingTickCmd arms the peer-sharing heartbeat.
//
// The beat is a fixed short interval, not the report cadence: whether a report
// is actually due is decided against m.nextReportAt when the beat lands. That
// split exists because a tea.Tick in flight cannot be re-timed, so baking the
// cadence into the timer would make every next_report_after take effect one
// report late.
//
// It is armed unconditionally in Init and re-armed on every beat, exactly like
// tickCmd, so there is only ever one lineage. Arming it on demand instead
// (when sharing is switched on) is the tempting version and the wrong one: a
// lineage from a previous switch-on is still in flight, so each off/on cycle
// would leave another live timer behind and add another report per interval.
// A beat with sharing off costs one no-op message.
func (m *Model) sharingTickCmd() tea.Cmd {
	return tea.Tick(telemetry.Heartbeat, func(t time.Time) tea.Msg { return sharingTickMsg(t) })
}

// sendReportCmd posts one report in the background.
//
// It takes the peer slice by value rather than reading m: Bubble Tea runs
// commands on their own goroutine, and closing over the Model would race the
// update loop.
func sendReportCmd(peers []rpc.PeerInfo, testnet bool, reporterID string) tea.Cmd {
	return func() tea.Msg {
		report, ok := telemetry.Build(peers, testnet, reporterID)
		if !ok {
			// Nothing worth sending: no outbound peers yet, or none of them
			// routable. Not an error, just a quiet no-op.
			return sharingDoneMsg{}
		}
		ack, err := telemetry.Post(telemetry.URL(), report)
		return sharingDoneMsg{ack: ack, err: err}
	}
}

// handleConsentKey drives the one-time peer-sharing question.
//
// Only three answers do anything, and there is deliberately no "decide
// later": leaving the question open would mean asking again next launch,
// which is how a consent prompt turns into nagging. Esc counts as no, the
// safe answer, and the one a user who just wants their wallet dashboard is
// implicitly giving.
func (m Model) handleConsentKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var share bool
	switch msg.String() {
	case "y", "Y":
		share = true
	case "n", "N", "esc", "q":
		share = false
	default:
		return m, nil
	}

	next, err := state.RecordConsent(m.state, share)
	if err != nil {
		// Could not persist. Honour the answer for this session rather than
		// trapping the user in the dialog, but do not pretend it stuck.
		m.sharingNote = "could not save your choice: " + err.Error()
		m.sharingOn = share
		m.conf.peerSharing = share
		m.mode = modeDashboard
		return m, nil
	}

	m.state = next
	m.sharingOn = share
	m.conf.peerSharing = share
	m.mode = modeDashboard
	if share {
		m.sharingNote = "peer sharing on, thank you"
		// No timer to start: the heartbeat armed in Init runs whatever the
		// answer is. Clearing the schedule makes the first report a full
		// interval from now rather than whenever a stale slot happened to be.
		m.nextReportAt = time.Time{}
	}
	return m, nil
}

// renderConsentModal asks, once, whether to share peer addresses.
//
// The screen is written to be readable by someone who did not go looking for
// this feature, because it opens unprompted on first launch. That means being
// concrete about three things: what leaves the machine, what does not, and
// how to change your mind. The "what does not" list is the important half:
// this is a wallet, and the honest answer to "is it sending my balance
// anywhere" needs to be visible rather than implied.
//
// Keep this wording and the payload in telemetry.go in step. If the report
// ever carries more than peer addresses, consentVersion in state.go must be
// bumped so people who agreed to this wording are asked again.
func (m Model) renderConsentModal() string {
	label := theme.Label.Render

	body := label("gridcoin.club publishes a list of reachable Gridcoin peers that") + "\n" +
		label("new wallets use to find the network. You can help keep it honest.") + "\n\n"

	body += theme.Title.Render("What would be sent") + "\n" +
		label("• the IP and port of peers this node connected OUT to") + "\n" +
		label("• a random ID your wallet makes up locally, so repeat reports") + "\n" +
		label("  can be counted without identifying you") + "\n" +
		label("• this wallet's version") + "\n\n"

	body += theme.Title.Render("What is never sent") + "\n" +
		theme.Good.Render("• your addresses, balances or transactions") + "\n" +
		theme.Good.Render("• your CPID, or anything about your BOINC work") + "\n" +
		theme.Good.Render("• your own IP address, or your peers' version strings") + "\n\n"

	body += label("Sent about once an hour, only while this program is open.") + "\n" +
		label("Change it any time with [c] → Peer sharing, or --peer-sharing=off.") + "\n\n"

	body += theme.Muted.Render("[y] Share my peers    [n] No thanks")

	modalWidth := 70
	if max := m.width - 4; modalWidth > max && max > 0 {
		modalWidth = max
	}
	modal := ui.ModalBox(modalWidth, "Help the peer list?", body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// onSharingTickMsg handles sharingTickMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onSharingTickMsg(msg sharingTickMsg) (tea.Model, tea.Cmd) {
	// Re-arm first so nothing below can break the lineage.
	cmds := []tea.Cmd{m.sharingTickCmd()}
	if m.sharingOn && m.state.ReporterID != "" {
		// Book the next slot as we dispatch rather than when the ack
		// lands, so a report that fails, or that never answers at all,
		// still retries on the normal cadence. A successful ack overwrites
		// it with whatever the collector asked for.
		next, send := telemetry.Schedule(time.Time(msg), m.nextReportAt, m.sharingInterval)
		m.nextReportAt = next
		if send {
			cmds = append(cmds, sendReportCmd(m.peers, m.cfg.Testnet, m.state.ReporterID))
		}
	}
	return m, tea.Batch(cmds...)
}

// onSharingDoneMsg handles sharingDoneMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onSharingDoneMsg(msg sharingDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// Quiet by design. A collector that is down is not the user's
		// problem, and the next tick will try again.
		m.sharingNote = "peer sharing: " + msg.err.Error()
	} else if msg.ack.Accepted > 0 {
		m.sharingNote = fmt.Sprintf("shared %d peers", msg.ack.Accepted)
		// Let the collector pace us rather than hard-coding the cadence,
		// and apply it to the next report rather than the one after it:
		// the beat that dispatched this report booked a slot at the old
		// interval, so overwrite it now that we know better.
		m.sharingInterval = telemetry.ClampInterval(msg.ack.NextReportAfter)
		m.nextReportAt = time.Now().Add(m.sharingInterval)
	}
	return m, nil
}
