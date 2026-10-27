// Stuck daemon: say so instead of showing "loading" forever.
//
// gridcoinresearchd opens its RPC port only after it has loaded the block
// index and the wallet, so a daemon that is still starting refuses the
// connection, and that already shows up as an error. A daemon that took the
// request and never answers is a different thing: usually a lock it never
// releases. Every call then waits out the client's 5-minute timeout, the
// refresh tick skips while fetches are out, and the panels sit on
// "loading wallet…" with nothing to say why.
//
// The rpc client lists the calls still waiting and since when the daemon has
// said nothing at all (rpc.Client.Pending). On every spinner tick we look at
// the dashboard's own calls, and once the daemon has been quiet for
// stallAfter while one of them waits, the footer and the stats panel say so.
package tui

import (
	"time"

	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
)

// stallAfter is how long the daemon may stay quiet before we say so. A
// healthy daemon answers the dashboard's calls in milliseconds; a busy one
// catching up on blocks takes seconds.
const stallAfter = 30 * time.Second

// dashboardMethods are the refresh calls that never legitimately take long.
// The send, sign and label dialogs make their own calls and are left out:
// they don't run the spinner, so nothing would sample them, and a stall
// note on the dashboard would blame the wrong thing.
var dashboardMethods = map[string]bool{
	"getwalletinfo":     true,
	"getblockchaininfo": true,
	"getstakinginfo":    true,
	"getpeerinfo":       true,
	"gettransaction":    true,
}

// slowMethods walk the whole wallet or chain and can take minutes on a big
// wallet. While one of ours is out, the daemon is busy, not stuck, and a
// quick call waiting behind its lock proves nothing.
var slowMethods = map[string]bool{
	"listreceivedbyaddress": true,
	"listsinceblock":        true,
	"listaccounts":          true,
	"getaddressesbyaccount": true,
	"listpolls":             true,
	"getpollresults":        true,
}

// daemonStall is the call the dashboard is waiting on and how long the daemon
// had been quiet when we last looked.
type daemonStall struct {
	method   string
	sent     bool
	quietFor time.Duration
}

// samplePending decides at now whether the daemon counts as stalled.
func (m *Model) samplePending(now time.Time) {
	m.stall, m.stalled = daemonStall{}, false
	if m.rpc == nil {
		return
	}
	calls, quietSince := m.rpc.Pending()
	m.stall, m.stalled = findStall(calls, quietSince, now)
}

// findStall is samplePending's rule: a dashboard call is waiting, none of
// our slow calls is out, and the daemon has been quiet for stallAfter.
func findStall(calls []rpc.Pending, quietSince, now time.Time) (daemonStall, bool) {
	oldest := -1
	for i, c := range calls {
		if slowMethods[c.Method] {
			return daemonStall{}, false
		}
		if dashboardMethods[c.Method] && (oldest < 0 || c.Since.Before(calls[oldest].Since)) {
			oldest = i
		}
	}
	if oldest < 0 {
		return daemonStall{}, false
	}
	quietFor := now.Sub(quietSince)
	if quietSince.IsZero() {
		quietFor = now.Sub(calls[oldest].Since)
	}
	if quietFor < stallAfter {
		return daemonStall{}, false
	}
	return daemonStall{method: calls[oldest].Method, sent: calls[oldest].Sent, quietFor: quietFor}, true
}

// daemonStalled reports whether to show the stall. While polling is paused
// nothing samples it, so the age would go stale; the pause note covers it.
func (m Model) daemonStalled() bool {
	return m.stalled && !m.paused
}

// stallShort fits the compact footer.
func (m Model) stallShort() string {
	quiet := format.FormatDuration(m.stall.quietFor)
	if !m.stall.sent {
		return "no connection " + quiet
	}
	return "no reply " + quiet
}

// stallNote is the one-line version, for the footer and under the stats.
func (m Model) stallNote() string {
	quiet := format.FormatDuration(m.stall.quietFor)
	if !m.stall.sent {
		return "no connection to the daemon for " + quiet
	}
	return "daemon hasn't answered for " + quiet + " (waiting on " + m.stall.method + ")"
}

// stallDetail replaces "loading wallet…" when the first fetch never came back.
func (m Model) stallDetail() string {
	quiet := format.FormatDuration(m.stall.quietFor)
	if !m.stall.sent {
		return "Still connecting to the daemon after " + quiet + ". The host isn't " +
			"answering at all: check the host, the port, and any firewall in between."
	}
	return "The daemon hasn't answered for " + quiet + "; the TUI is waiting on " +
		m.stall.method + ". A daemon that is still starting refuses the connection " +
		"instead, so this one is either busy with something long, such as another " +
		"program's call, or stuck. Its debug.log shows what it did last."
}
