package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
)

func TestFindStall(t *testing.T) {
	now := time.Now()
	call := func(method string, age time.Duration) rpc.Pending {
		return rpc.Pending{Method: method, Since: now.Add(-age), Sent: true}
	}
	cases := []struct {
		name  string
		calls []rpc.Pending
		quiet time.Duration // 0: the daemon has not gone quiet before these calls
		want  bool
	}{
		{"nothing out", nil, 0, false},
		{"just started", []rpc.Pending{call("getwalletinfo", time.Second)}, 0, false},
		{"just under", []rpc.Pending{call("getwalletinfo", stallAfter-time.Millisecond)}, 0, false},
		{"at the limit", []rpc.Pending{call("getwalletinfo", stallAfter)}, 0, true},
		{"long gone", []rpc.Pending{call("getpeerinfo", 10*time.Minute)}, 0, true},
		// The call is fresh, but it replaced one that timed out: the daemon
		// has been silent all along.
		{"after a timeout", []rpc.Pending{call("getblockchaininfo", time.Second)}, 6 * time.Minute, true},
		// One of our own long scans holds the wallet lock: busy, not stuck.
		{"behind a scan", []rpc.Pending{
			call("listreceivedbyaddress", 4*time.Minute),
			call("gettransaction", 2*time.Minute),
		}, 4 * time.Minute, false},
		// Dialog calls are not the dashboard's to report.
		{"dialog call", []rpc.Pending{call("walletpassphrase", time.Hour)}, time.Hour, false},
	}
	for _, tc := range cases {
		var quietSince time.Time
		if tc.quiet > 0 {
			quietSince = now.Add(-tc.quiet)
		}
		_, got := findStall(tc.calls, quietSince, now)
		if got != tc.want {
			t.Errorf("%s: stalled = %v, want %v", tc.name, got, tc.want)
		}
	}

	s, _ := findStall([]rpc.Pending{call("getblockchaininfo", time.Second)}, now.Add(-7*time.Minute), now)
	if s.method != "getblockchaininfo" || s.quietFor != 7*time.Minute {
		t.Errorf("stall = %+v, want getblockchaininfo quiet for 7m", s)
	}
}

// stalledModel is a dashboard waiting on method while the daemon has been
// quiet for quietFor.
func stalledModel(method string, quietFor time.Duration) Model {
	return Model{
		width:    300,
		inflight: 1,
		stall:    daemonStall{method: method, sent: true, quietFor: quietFor},
		stalled:  true,
	}
}

// The first getwalletinfo never came back: instead of "loading wallet…" the
// stats panel says the daemon is quiet and why that is not a slow start.
func TestStallReplacesLoading(t *testing.T) {
	m := stalledModel("getwalletinfo", 2*time.Minute+10*time.Second)
	out := m.renderStats()
	if strings.Contains(out, "loading wallet") {
		t.Errorf("still says loading:\n%s", out)
	}
	for _, want := range []string{"hasn't answered for 2m10s", "getwalletinfo", "busy with something long", "or stuck"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats missing %q:\n%s", want, out)
		}
	}

	m.stall.sent = false
	if out := m.renderStats(); !strings.Contains(out, "Still connecting to the daemon after 2m10s") {
		t.Errorf("unsent call should read as a connection problem:\n%s", out)
	}

	m.stalled = false
	if out := m.renderStats(); !strings.Contains(out, "loading wallet") {
		t.Errorf("no stall should still read as loading:\n%s", out)
	}
}

// With data already on screen, the numbers stay and a note under them and in
// the footer says they are going stale; the spinner gives way to it.
func TestStallNoteOnLoadedDashboard(t *testing.T) {
	m := stalledModel("getblockchaininfo", 45*time.Second)
	m.loaded = true
	want := "daemon hasn't answered for 45s (waiting on getblockchaininfo)"
	if out := m.renderStats(); !strings.Contains(out, want) {
		t.Errorf("stats missing %q:\n%s", want, out)
	}
	if out := m.renderStatusBar(nil); !strings.Contains(out, want) {
		t.Errorf("footer missing %q:\n%s", want, out)
	}

	// Paused, nothing samples the stall, so its age would go stale: the
	// pause note takes over everywhere.
	m.paused = true
	if out := m.renderStatusBar(nil); strings.Contains(out, want) || !strings.Contains(out, "paused") {
		t.Errorf("a paused footer should show the pause, not the stall:\n%s", out)
	}
	if out := m.renderStats(); strings.Contains(out, want) {
		t.Errorf("a paused dashboard should not show a stale stall:\n%s", out)
	}
}

func TestStallNoteCompact(t *testing.T) {
	m := stalledModel("getwalletinfo", time.Minute)
	m.width = 60
	if out := m.renderCompactStats(); !strings.Contains(out, "or stuck") {
		t.Errorf("compact loading box missing the stall:\n%s", out)
	}
	m.loaded = true
	m.width = 300
	if out := m.renderCompactStats(); !strings.Contains(out, "daemon hasn't answered for 1m00s") {
		t.Errorf("compact stats missing the stall note:\n%s", out)
	}
	m.width = 40
	if out := m.renderCompactFooter(); !strings.Contains(out, "no reply 1m00s") {
		t.Errorf("compact footer missing the stall:\n%s", out)
	}
}

// Once every fetch is back the spinner chain stops and the stall goes with it.
func TestSpinnerStopClearsStall(t *testing.T) {
	m := stalledModel("getwalletinfo", time.Hour)
	m.inflight = 0
	m.spinnerRunning = true
	next, _ := m.Update(spinnerTickMsg(time.Now()))
	if next.(Model).daemonStalled() {
		t.Error("stall survived every fetch coming back")
	}
}
