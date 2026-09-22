// Tests for the polls feature: the HR-date parsing / time-left formatting
// helpers and the listpolls / getpollresults JSON decoding. See rpc_test.go
// for a testing primer.
package tui

import (
	"strings"
	"testing"

	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
)

// TestRenderPollsScreen drives the full-screen renderer end-to-end: it must
// not panic (the lazy-tally maps must be safe to read even when empty), and a
// poll with a cached result should show its participation + leading answer
// while an uncached one falls back to the raw vote count.
func TestRenderPollsScreen(t *testing.T) {
	pct := 62.4
	m := Model{
		width:             100,
		height:            24,
		mode:              modePolls,
		pollsLoaded:       true,
		pollsShowFinished: true,
		polls: []rpc.Poll{
			{Title: "Fund outreach 2026", ID: "a", WeightType: "Magnitude", Expiration: "12-31-2999 23:59:59", Votes: 14},
			{Title: "Raise block reward", ID: "b", WeightType: "Balance", Expiration: "01-01-2000 00:00:00", Votes: 9},
		},
		pollResults: map[string]rpc.PollResult{
			"a": {VotePercentAVW: &pct, TopChoice: "Yes"},
		},
		pollResultPending: map[string]bool{},
	}

	out := m.View()
	if !strings.Contains(out, "Polls") {
		t.Error("output missing the Polls title")
	}
	if !strings.Contains(out, "Fund outreach 2026") || !strings.Contains(out, "Raise block reward") {
		t.Error("output missing a poll row")
	}
	if !strings.Contains(out, "62%") || !strings.Contains(out, "Yes") {
		t.Errorf("cached poll should show participation + leader, got:\n%s", out)
	}
	if !strings.Contains(out, "9 votes") {
		t.Errorf("uncached poll should show the raw vote count, got:\n%s", out)
	}
	if !strings.Contains(out, "ended") {
		t.Error("the year-2000 poll should render as ended")
	}
}

// TestRenderPollDetailModal drives the detail popup end-to-end: metadata,
// participation, and the per-choice results breakdown (labels, percentages,
// bar glyphs) all render from the cached tally.
func TestRenderPollDetailModal(t *testing.T) {
	pct := 62.0
	m := Model{
		width:      100,
		height:     30,
		mode:       modePollDetail,
		pollCursor: 0,
		polls: []rpc.Poll{{
			Title: "Fund outreach 2026", ID: "a", Question: "Should we fund outreach?",
			URL: "https://gridcoin.us/x", WeightType: "Magnitude", ResponseType: "Yes/No/Abstain",
			DurationDays: 7, Timestamp: "03-02-2026 14:05:06", Expiration: "12-31-2999 23:59:59", Votes: 14,
		}},
		pollResults: map[string]rpc.PollResult{
			"a": {
				VotePercentAVW: &pct, TopChoice: "Yes", TotalWeight: 100,
				Responses: []rpc.PollResponse{
					{Choice: "Yes", Weight: 62, Votes: 9},
					{Choice: "No", Weight: 38, Votes: 3},
				},
			},
		},
		pollResultPending: map[string]bool{},
	}

	out := m.View()
	for _, want := range []string{"Fund outreach 2026", "Should we fund outreach?", "62.0% AVW", "Results", "Yes", "No", "62% share", "62 weight", "9.00 votes", "█"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail popup missing %q, got:\n%s", want, out)
		}
	}
}

// TestRenderPollDetailModalTallyPending shows the loading placeholder when the
// selected poll's tally hasn't landed yet.
func TestRenderPollDetailModalTallyPending(t *testing.T) {
	m := Model{
		width: 100, height: 30, mode: modePollDetail, pollCursor: 0,
		polls:             []rpc.Poll{{Title: "Pending poll", ID: "p", Expiration: "12-31-2999 23:59:59"}},
		pollResults:       map[string]rpc.PollResult{},
		pollResultPending: map[string]bool{"p": true},
	}
	out := m.View()
	if !strings.Contains(out, "tallying…") {
		t.Errorf("expected 'tallying…' placeholder, got:\n%s", out)
	}
}

// TestRenderPollDetailModalTallyError shows the error (and a retry hint) when
// the getpollresults tally failed, instead of a perpetual "tallying…".
func TestRenderPollDetailModalTallyError(t *testing.T) {
	m := Model{
		width: 100, height: 30, mode: modePollDetail, pollCursor: 0,
		polls:             []rpc.Poll{{Title: "Broken poll", ID: "b", Expiration: "12-31-2999 23:59:59"}},
		pollResults:       map[string]rpc.PollResult{},
		pollResultPending: map[string]bool{},
		pollResultErr:     map[string]string{"b": "reorg during tally"},
	}
	out := m.View()
	if strings.Contains(out, "tallying…") {
		t.Errorf("errored tally should not show the loading placeholder, got:\n%s", out)
	}
	if !strings.Contains(out, "reorg during tally") || !strings.Contains(out, "retry") {
		t.Errorf("expected the error text and a retry hint, got:\n%s", out)
	}
}

// TestReloadPollsInvalidatesTallies checks that a reload (r / reopen / toggle)
// drops the cached getpollresults tallies so stale participation %/leader don't
// survive a refresh. rpc is nil on purpose: reloadPolls only builds a Cmd, it
// doesn't run the fetch here.
func TestReloadPollsInvalidatesTallies(t *testing.T) {
	pct := 50.0
	m := Model{
		pollsShowFinished: true,
		pollResults:       map[string]rpc.PollResult{"a": {VotePercentAVW: &pct}},
		pollResultPending: map[string]bool{"b": true},
		pollResultErr:     map[string]string{"c": "boom"},
	}
	_ = m.reloadPolls()
	if len(m.pollResults) != 0 || len(m.pollResultPending) != 0 || len(m.pollResultErr) != 0 {
		t.Errorf("reloadPolls should clear the tally caches, got results=%d pending=%d err=%d",
			len(m.pollResults), len(m.pollResultPending), len(m.pollResultErr))
	}
}

// TestPollSettleFetchesOnlyStillSelected checks the debounce: a settle timer
// starts the tally only when the cursor is still on the poll that armed it.
func TestPollSettleFetchesOnlyStillSelected(t *testing.T) {
	newModel := func() Model {
		return Model{
			polls:             []rpc.Poll{{Title: "a", ID: "a"}, {Title: "b", ID: "b"}},
			pollCursor:        0, // cursor rests on "a"
			pollResults:       map[string]rpc.PollResult{},
			pollResultPending: map[string]bool{},
			pollResultErr:     map[string]string{},
		}
	}

	// Timer for the still-selected poll "a" starts its tally (pending set).
	m := newModel()
	next, _ := m.Update(pollSettleMsg{id: "a"})
	if !next.(Model).pollResultPending["a"] {
		t.Error("settle for the selected poll should start its tally")
	}

	// Timer for "b" (cursor has since left it) does nothing.
	m2 := newModel()
	next2, _ := m2.Update(pollSettleMsg{id: "b"})
	if len(next2.(Model).pollResultPending) != 0 {
		t.Errorf("settle for a no-longer-selected poll should not fetch, pending=%v",
			next2.(Model).pollResultPending)
	}
}

// TestPollsMsgSortsNewestFirst checks that a loaded poll list is ordered by
// posting date, most recent first, regardless of the daemon's order.
func TestPollsMsgSortsNewestFirst(t *testing.T) {
	m := Model{
		pollsShowFinished: true,
		pollResults:       map[string]rpc.PollResult{},
		pollResultPending: map[string]bool{},
		pollResultErr:     map[string]string{},
	}
	msg := pollsMsg{includeFinished: true, polls: []rpc.Poll{
		{Title: "middle", Timestamp: "03-05-2026 10:00:00"},
		{Title: "oldest", Timestamp: "01-01-2026 10:00:00"},
		{Title: "newest", Timestamp: "06-30-2026 10:00:00"},
		{Title: "undated", Timestamp: "not a date"},
	}}
	next, _ := m.Update(msg)
	got := next.(Model)
	order := []string{got.polls[0].Title, got.polls[1].Title, got.polls[2].Title, got.polls[3].Title}
	want := []string{"newest", "middle", "oldest", "undated"}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("poll order = %v, want %v", order, want)
			break
		}
	}
}

// TestPollsMsgIgnoresStaleScope checks that a listpolls reply whose scope no
// longer matches the screen (user toggled all/active mid-flight) is discarded
// rather than overwriting the current view.
func TestPollsMsgIgnoresStaleScope(t *testing.T) {
	current := []rpc.Poll{{Title: "active poll", ID: "keep"}}
	m := Model{
		pollsShowFinished: false, // screen is showing "active only"
		pollsLoaded:       true,
		polls:             current,
		pollResults:       map[string]rpc.PollResult{},
		pollResultPending: map[string]bool{},
	}

	// A late "all polls" (includeFinished=true) response must NOT replace the
	// active-only list currently on screen.
	stale := pollsMsg{includeFinished: true, polls: []rpc.Poll{{Title: "finished poll", ID: "stale"}}}
	next, _ := m.Update(stale)
	got := next.(Model)
	if len(got.polls) != 1 || got.polls[0].ID != "keep" {
		t.Errorf("stale-scope reply should be ignored, got %+v", got.polls)
	}

	// A matching-scope response IS applied.
	fresh := pollsMsg{includeFinished: false, polls: []rpc.Poll{{Title: "new active", ID: "fresh"}}}
	next2, _ := got.Update(fresh)
	got2 := next2.(Model)
	if len(got2.polls) != 1 || got2.polls[0].ID != "fresh" {
		t.Errorf("matching-scope reply should be applied, got %+v", got2.polls)
	}
}
