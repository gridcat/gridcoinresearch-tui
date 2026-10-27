// Terminal probe: pause polling while no terminal is attached.
//
// A `docker exec -it` session outlives the ssh connection that started it:
// containerd-shim holds the pty, so the TUI gets no SIGHUP and no EOF and
// would poll the daemon forever for nobody. Keypresses can't tell us anyone
// left either, because people watch this dashboard for days without typing.
//
// So we ask the terminal instead. Every probeInterval we write Primary Device
// Attributes (ESC [ c). Every real terminal, tmux and screen included, answers
// on its own with ESC [ ? … c, without anyone pressing a key; an orphaned pty
// has nobody to answer. After probeMisses unanswered probes polling pauses, and
// the next answer (or key) resumes it. A terminal that never answered at all
// is assumed not to support the query and is never paused.
package tui

import (
	"bytes"
	"os"
	"reflect"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	probeInterval = time.Minute
	probeMisses   = 3
)

// probeQuery is Primary Device Attributes.
const probeQuery = "\x1b[c"

// probeTickMsg fires every probeInterval.
type probeTickMsg time.Time

// probeWriting guards against stacking writes on a pty nobody drains: a
// blocked write would otherwise leak one goroutine per probe, forever.
var probeWriting atomic.Bool

func probeTickCmd() tea.Cmd {
	return tea.Tick(probeInterval, func(t time.Time) tea.Msg { return probeTickMsg(t) })
}

// sendProbeCmd writes the query straight to stdout, which is where Bubble Tea
// renders. Bubble Tea v1 has no API for raw output; three bytes between frames
// are harmless.
func sendProbeCmd() tea.Cmd {
	return func() tea.Msg {
		if !probeWriting.CompareAndSwap(false, true) {
			return nil
		}
		defer probeWriting.Store(false)
		_, _ = os.Stdout.WriteString(probeQuery)
		return nil
	}
}

// isProbeReply reports whether msg is a terminal's answer to probeQuery.
// Bubble Tea v1 delivers it as its unexported unknownCSISequenceMsg, a
// []byte holding the raw sequence, so we match on the bytes.
func isProbeReply(msg tea.Msg) bool {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem().Kind() != reflect.Uint8 {
		return false
	}
	b := v.Bytes()
	return bytes.HasPrefix(b, []byte("\x1b[?")) && bytes.HasSuffix(b, []byte("c"))
}

// onProbeTickMsg pauses once probeMisses probes in a row went unanswered,
// then sends the next probe. Probing continues while paused, so a terminal
// that comes back (a laptop waking with its ssh session intact) answers and
// resumes polling without a keypress.
func (m Model) onProbeTickMsg(msg probeTickMsg) (tea.Model, tea.Cmd) {
	if m.termAnswered && m.probesUnanswered >= probeMisses && !m.paused {
		m.paused = true
		m.pausedAt = time.Time(msg)
	}
	m.probesUnanswered++
	return m, tea.Batch(probeTickCmd(), sendProbeCmd())
}

// termAlive records proof that a terminal is attached (a probe reply or a
// key) and resumes polling if it was paused.
func (m Model) termAlive(isReply bool) (Model, tea.Cmd) {
	if isReply {
		m.termAnswered = true
	}
	m.probesUnanswered = 0
	if !m.paused {
		return m, nil
	}
	m.paused = false
	m.pausedAt = time.Time{}
	var cmds []tea.Cmd
	// Same anti-pileup rule as the tick: if fetches from before the pause
	// are still out (stalled daemon), don't stack another sequence on them.
	if m.inflight == 0 {
		cmds = append(cmds, m.refreshAllCmd(), m.bumpInflight(6))
	}
	// The tick chain ends at the first tick after the pause. If that tick
	// hasn't fired yet the chain is still live, and a second one would
	// double the refresh rate.
	if !m.tickRunning {
		m.tickRunning = true
		cmds = append(cmds, m.tickCmd())
	}
	return m, tea.Batch(cmds...)
}
