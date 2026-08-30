package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The peer-sharing scheduler. Four things have to hold: the launch-only opt-in
// can actually report, exactly one heartbeat lineage exists however the setting
// is flipped, a cadence from the collector applies to the next report rather
// than the one after it, and peers cached from one daemon are never filed under
// another.
//
// Note what these tests never do: run a tea.Cmd. tea.Batch collapses to the
// single command when given one, so executing the result of a beat would call
// the real tea.Tick and block for a full heartbeat. The scheduling decision is
// pure (scheduleReport) precisely so it can be checked without that.

func TestLaunchOnlyOptInGetsAReporterID(t *testing.T) {
	// A container started with GRC_PEER_SHARING=on has no stored consent and so
	// no stored identifier. Without one, every report is dropped before it is
	// built and the documented opt-in silently does nothing.
	t.Setenv("GRC_STATE_DIR", t.TempDir())

	m := NewModel(Config{PeerSharing: PeerSharingOn}, nil)
	if !m.sharingOn {
		t.Fatal("sharing should be on when the flag says so")
	}
	if len(m.state.ReporterID) != 32 {
		t.Fatalf("reporter id = %q, want 32 hex chars; without one every report is dropped", m.state.ReporterID)
	}
	// The answer itself must not be recorded, or the human is never asked.
	if got := loadState().PeerSharing; got != PeerSharingUnset {
		t.Errorf("stored answer = %q, want unset so the consent screen still shows", got)
	}
	// The identifier is persisted, so a restart is the same vantage point.
	if loadState().ReporterID != m.state.ReporterID {
		t.Error("reporter id not persisted: every restart would look like a new reporter")
	}
}

func TestLaunchOnlyOptOutMintsNothing(t *testing.T) {
	t.Setenv("GRC_STATE_DIR", t.TempDir())

	if m := NewModel(Config{PeerSharing: PeerSharingOff}, nil); m.state.ReporterID != "" {
		t.Error("declining should not mint an identifier")
	}
}

func TestHeartbeatIsArmedRegardlessOfConsent(t *testing.T) {
	// The lineage must not depend on the answer. If it did, switching sharing
	// on later would have to arm a second one, and every off/on cycle would
	// leave another live timer behind.
	t.Setenv("GRC_STATE_DIR", t.TempDir())

	for _, share := range []PeerSharing{PeerSharingOn, PeerSharingOff} {
		m := NewModel(Config{PeerSharing: share}, nil)
		if m.sharingTickCmd() == nil {
			t.Errorf("%s: no heartbeat armed", share)
		}
	}
}

func TestScheduleReportBooksBeforeItSends(t *testing.T) {
	now := time.Now()

	// Nothing booked yet: the first beat of a run, or the first after sharing
	// was switched on. Schedule, do not send.
	next, send := scheduleReport(now, time.Time{}, time.Hour)
	if send {
		t.Error("an unscheduled beat must not send")
	}
	if want := now.Add(time.Hour); !next.Equal(want) {
		t.Errorf("booked %v, want %v", next, want)
	}

	// Booked but not yet due: leave the slot alone.
	booked := now.Add(20 * time.Minute)
	next, send = scheduleReport(now, booked, time.Hour)
	if send || !next.Equal(booked) {
		t.Errorf("early beat: send=%v next=%v, want no send and the slot untouched", send, next)
	}

	// Due: send, and rebook at dispatch so a report that never answers still
	// retries on the normal cadence.
	next, send = scheduleReport(now, now.Add(-time.Second), 30*time.Minute)
	if !send {
		t.Error("a due beat must send")
	}
	if want := now.Add(30 * time.Minute); !next.Equal(want) {
		t.Errorf("rebooked %v, want %v", next, want)
	}
}

func TestScheduleReportFallsBackToTheDefaultCadence(t *testing.T) {
	now := time.Now()
	next, _ := scheduleReport(now, time.Time{}, 0)
	if want := now.Add(telemetryInterval); !next.Equal(want) {
		t.Errorf("booked %v, want the default interval %v", next, want)
	}
}

func TestBeatDoesNotReportWhenSharingIsOff(t *testing.T) {
	booked := time.Now().Add(-time.Hour) // long overdue
	m := Model{
		sharingOn:    false,
		state:        State{ReporterID: "a1b2c3d4e5f60718293a4b5c6d7e8f90"},
		nextReportAt: booked,
	}

	next, cmd := m.Update(sharingTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("the heartbeat must re-arm even with sharing off, or it dies for the rest of the run")
	}
	if got := next.(Model).nextReportAt; !got.Equal(booked) {
		t.Error("a beat with sharing off must not touch the schedule")
	}
}

func TestBeatWithoutAReporterIDDoesNotReport(t *testing.T) {
	m := Model{sharingOn: true, nextReportAt: time.Now().Add(-time.Hour)}

	next, cmd := m.Update(sharingTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("the heartbeat must re-arm")
	}
	if !next.(Model).nextReportAt.Equal(m.nextReportAt) {
		t.Error("no identifier means nothing to schedule against")
	}
}

func TestLaunchOnlyOptInActuallyDispatchesAReport(t *testing.T) {
	// The whole chain for a container opt-in, end to end: NewModel resolves
	// sharing on and mints an identifier, a due beat dispatches a report, and
	// the heartbeat survives. This is the case that silently did nothing.
	t.Setenv("GRC_STATE_DIR", t.TempDir())

	m := NewModel(Config{PeerSharing: PeerSharingOn}, nil)
	m.peers = []PeerInfo{{Addr: "45.33.32.156:32749"}}
	m.nextReportAt = time.Now().Add(-time.Second)

	_, cmd := m.Update(sharingTickMsg(time.Now()))

	// tea.Batch collapses to the bare command when given one, and one command
	// is exactly the regression here: the heartbeat re-armed, no report sent.
	// Running it would then block for a whole heartbeat, so this waits on it
	// instead of calling it inline. With two commands Batch hands back a
	// BatchMsg without executing either, so nothing ticks and nothing posts.
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()

	select {
	case msg := <-got:
		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			t.Fatalf("a due beat produced a single %T, not a batch", msg)
		}
		if len(batch) != 2 {
			t.Errorf("got %d commands, want the heartbeat plus one report", len(batch))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the beat produced only the heartbeat: no report was dispatched")
	}
}

func TestAckAppliesCadenceToTheNextReport(t *testing.T) {
	// The beat books a slot at the old interval as it dispatches. If the ack
	// only updated sharingInterval, a collector asking for 24 h would still be
	// reported to an hour later, and every change would land one report late.
	m := Model{sharingOn: true, sharingInterval: time.Hour, nextReportAt: time.Now().Add(time.Hour)}

	next, _ := m.Update(sharingDoneMsg{ack: reportAck{OK: true, Accepted: 3, NextReportAfter: 24 * 3600}})
	got := next.(Model)
	if got.sharingInterval != telemetryMaxInterval {
		t.Errorf("interval = %v, want %v", got.sharingInterval, telemetryMaxInterval)
	}
	if until := time.Until(got.nextReportAt); until < 23*time.Hour {
		t.Errorf("next report in %v, want about 24h: the new cadence landed one report late", until)
	}
}

func TestFailedReportKeepsTheSlotBookedAtDispatch(t *testing.T) {
	booked := time.Now().Add(time.Hour)
	m := Model{sharingOn: true, sharingInterval: time.Hour, nextReportAt: booked}

	next, _ := m.Update(sharingDoneMsg{err: errFake})
	if got := next.(Model).nextReportAt; !got.Equal(booked) {
		t.Error("a failed report must keep the slot the beat booked, so the retry is on cadence")
	}
}

func TestSwitchingSharingOnStartsAFreshInterval(t *testing.T) {
	// A slot left over from the last time sharing was on must not make the
	// first report after re-enabling fire straight away.
	t.Setenv("GRC_STATE_DIR", t.TempDir())

	m := NewModel(Config{}, nil)
	m.sharingOn = false
	m.nextReportAt = time.Now().Add(-time.Hour)
	m.conf.peerSharing = true

	if !applied(t, m).nextReportAt.IsZero() {
		t.Error("enabling sharing should clear the stale slot")
	}
}

func TestSwitchingSharingOnArmsNoSecondTimer(t *testing.T) {
	// The subtle one. Arming a heartbeat here looks harmless, but the lineage
	// from Init is still running, so each off/on cycle would leave another live
	// timer behind and add another report per interval. Apply must return the
	// same two commands whether or not the sharing row changed.
	t.Setenv("GRC_STATE_DIR", t.TempDir())

	count := func(m Model) int {
		t.Helper()
		m.conf.host.SetValue("127.0.0.1")
		m.conf.port.SetValue("15715")
		m.conf.refresh.SetValue("10s")
		_, cmd := m.applyConfig()
		// Neither branch can block here: with the fix Apply returns a single
		// sequence, and the regression would make it a batch. tea.Sequence and
		// tea.Batch both wrap their children without running them.
		if batch, ok := cmd().(tea.BatchMsg); ok {
			return len(batch)
		}
		return 1
	}

	unchanged := NewModel(Config{}, nil)

	toggled := NewModel(Config{}, nil)
	toggled.sharingOn = false
	toggled.conf.peerSharing = true

	if got, want := count(toggled), count(unchanged); got != want {
		t.Errorf("apply returned %d commands when sharing was switched on, want %d: a second heartbeat lineage was armed", got, want)
	}
}

func TestApplyConfigDropsCachedPeers(t *testing.T) {
	// The cached peers belong to the daemon we just stopped talking to. Sending
	// them after a network toggle files mainnet peers as testnet ones.
	t.Setenv("GRC_STATE_DIR", t.TempDir())

	m := NewModel(Config{}, nil)
	m.peers = []PeerInfo{{Addr: "45.33.32.156:32749"}}
	m.peersLoaded = true

	if got := applied(t, m).peers; got != nil {
		t.Errorf("peers survived an endpoint change: %v would be reported under the new network tag", got)
	}
}

// applied fills the config form with something valid and applies it. Without
// valid values applyConfig bails at the first check and never reaches the cache
// flush or the sharing block these tests are about.
func applied(t *testing.T, m Model) Model {
	t.Helper()
	m.conf.host.SetValue("127.0.0.1")
	m.conf.port.SetValue("15715")
	m.conf.refresh.SetValue("10s")
	out, _ := m.applyConfig()
	got := out.(Model)
	if got.conf.errMsg != "" {
		t.Fatalf("config form rejected: %s", got.conf.errMsg)
	}
	return got
}
