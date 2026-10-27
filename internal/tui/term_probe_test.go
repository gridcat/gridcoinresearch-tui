// Tests for the terminal probe: polling pauses after probeMisses unanswered
// probes and resumes on the next reply or key, without ever running two tick
// chains.
package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gridcat/gridcoinresearch-tui/internal/config"
)

// csiReply stands in for Bubble Tea's unexported unknownCSISequenceMsg, which
// is how a probe reply arrives: a named []byte holding the raw sequence.
type csiReply []byte

// probedModel is a settled dashboard whose terminal answered once.
func probedModel() Model {
	m := NewModel(config.Config{Refresh: 10 * time.Second}, nil)
	m.mode = modeDashboard
	m.inflight = 0
	m.spinnerRunning = false
	m.width, m.height = 120, 40
	m.termAnswered = true
	return m
}

func probeTicks(m Model, n int) Model {
	for i := 0; i < n; i++ {
		next, _ := m.Update(probeTickMsg(time.Now()))
		m = next.(Model)
	}
	return m
}

func TestIsProbeReply(t *testing.T) {
	for in, want := range map[string]bool{
		"\x1b[?62;22c": true,  // xterm-style DA1
		"\x1b[?1;2c":   true,  // VT100
		"\x1b[12;40R":  false, // cursor position report
		"\x1b[c":       false, // no "?": not a DA1 reply
	} {
		if got := isProbeReply(csiReply(in)); got != want {
			t.Errorf("isProbeReply(%q) = %v, want %v", in, got, want)
		}
	}
	if isProbeReply(tea.KeyMsg{Type: tea.KeyEnter}) || isProbeReply("\x1b[?62c") {
		t.Error("only a byte-slice message can be a reply")
	}
}

func TestUnansweredProbesPause(t *testing.T) {
	m := probeTicks(probedModel(), probeMisses)
	if m.paused {
		t.Fatalf("paused after %d probes; the pause needs %d misses", probeMisses, probeMisses)
	}
	m = probeTicks(m, 1)
	if !m.paused || m.pausedAt.IsZero() {
		t.Fatal("should pause once probeMisses probes went unanswered")
	}
	if !strings.Contains(m.renderFooter(), "terminal not answering") {
		t.Error("footer should say why polling is paused")
	}

	// The next refresh tick ends the chain without fetching.
	next, cmd := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if cmd != nil || m.inflight != 0 || m.tickRunning {
		t.Fatalf("paused tick: cmd=%v inflight=%d tickRunning=%v, want the chain to end", cmd != nil, m.inflight, m.tickRunning)
	}
}

func TestRepliesKeepPolling(t *testing.T) {
	m := probedModel()
	for i := 0; i < 10; i++ {
		m = probeTicks(m, 1)
		next, _ := m.Update(csiReply("\x1b[?62;22c"))
		m = next.(Model)
	}
	if m.paused {
		t.Fatal("a terminal that keeps answering must never pause")
	}
}

func TestSilentTerminalNeverPauses(t *testing.T) {
	m := probedModel()
	m.termAnswered = false
	if m = probeTicks(m, 10); m.paused {
		t.Fatal("a terminal that never answered is assumed not to support the probe")
	}
}

func TestReplyResumesWithOneTickChain(t *testing.T) {
	m := probeTicks(probedModel(), probeMisses+1)
	next, _ := m.Update(tickMsg(time.Now())) // chain ends
	m = next.(Model)

	next, cmd := m.Update(csiReply("\x1b[?62;22c"))
	m = next.(Model)
	if m.paused || cmd == nil || m.inflight != 6 || !m.tickRunning {
		t.Fatalf("resume: paused=%v cmd=%v inflight=%d tickRunning=%v, want a full refresh and a new chain", m.paused, cmd != nil, m.inflight, m.tickRunning)
	}
}

func TestResumeBeforeTickKeepsLiveChain(t *testing.T) {
	m := probeTicks(probedModel(), probeMisses+1)
	// Reply lands before the pending refresh tick: that chain is still
	// live, so resuming must not start another.
	next, _ := m.Update(csiReply("\x1b[?62;22c"))
	m = next.(Model)
	if m.paused || !m.tickRunning {
		t.Fatalf("paused=%v tickRunning=%v", m.paused, m.tickRunning)
	}
	next, cmd := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if cmd == nil || m.inflight == 0 {
		t.Fatal("the live chain should carry on polling")
	}
}

func TestResumeDoesNotStackOnStuckFetches(t *testing.T) {
	m := probeTicks(probedModel(), probeMisses+1)
	m.inflight = 3 // a stalled daemon still holds fetches from before the pause

	next, _ := m.Update(csiReply("\x1b[?62;22c"))
	m = next.(Model)
	if m.paused || m.inflight != 3 {
		t.Fatalf("resume: paused=%v inflight=%d, want resumed with no new fetches", m.paused, m.inflight)
	}
}

func TestKeyResumesAndActs(t *testing.T) {
	m := probeTicks(probedModel(), probeMisses+1)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = next.(Model)
	if m.paused || cmd == nil {
		t.Fatalf("key: paused=%v cmd=%v, want resume", m.paused, cmd != nil)
	}
	if m.mode != modeSend {
		t.Fatalf("the key should still do its job: mode=%d", m.mode)
	}
}
