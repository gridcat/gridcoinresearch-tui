// Peer sharing: the opt-in half of gridcoin.club's addnodes list.
//
// When the user turns it on, the TUI periodically posts the addresses of the
// peers this node dialled OUT to. Those addresses feed addnodes.gridcoin.club,
// the community peer-seed list a fresh wallet uses to find its first peers.
// What it buys the network is a second vantage point: a node reachable from
// the list's own server may still be firewalled from where you are, and only
// a real wallet somewhere else can tell the difference.
//
// This file holds pure transforms only — no HTTP, no disk. The side effects
// live in telemetry_client.go and state.go, mirroring how updatecheck.go and
// selfupdate.go are split. That keeps everything here unit-testable without a
// socket.
//
// What leaves the machine is deliberately tiny, and the consent screen in
// view.go promises exactly this shape. Widening it later means revisiting
// that promise, not just editing a struct.
package main

import (
	"net"
	"sort"
	"strconv"
	"time"
)

const (
	// telemetryVersion is the payload version the server validates against.
	telemetryVersion = 1

	// maxSharedPeers caps one report. A wallet holds a handful of outbound
	// connections; anything near this is already pathological.
	maxSharedPeers = 64
)

// peerReport is the exact JSON body posted to the collector. Addresses and
// nothing else: no wallet addresses, no balances, no transactions, no CPID,
// and not even the peers' version strings.
type peerReport struct {
	Version  int      `json:"v"`
	Reporter string   `json:"reporter"`
	Client   string   `json:"client"`
	Network  string   `json:"network"`
	Peers    []string `json:"peers"`
}

// reportAck is the collector's answer. NextReportAfter lets the server pace
// clients without us shipping a new binary.
type reportAck struct {
	OK              bool `json:"ok"`
	Accepted        int  `json:"accepted"`
	Rejected        int  `json:"rejected"`
	NextReportAfter int  `json:"next_report_after"`
}

// nonRoutable lists the ranges a public Gridcoin peer can never live in.
// Anything here would either point back at the reporter's own network or at
// documentation space, so sending it would be noise at best and a way to make
// the collector probe someone's LAN at worst.
//
// Parsed once at init: net.ParseCIDR on every peer on every tick would be
// wasteful, and a malformed constant should fail loudly at startup rather
// than silently letting an address through.
var nonRoutable = mustParseCIDRs([]string{
	"100.64.0.0/10",   // CGNAT
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"240.0.0.0/4",     // reserved
	"2001:db8::/32",   // IPv6 documentation
})

func mustParseCIDRs(list []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(list))
	for _, s := range list {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic("telemetry: bad CIDR constant " + s + ": " + err.Error())
		}
		out = append(out, n)
	}
	return out
}

// isShareableIP reports whether an address is one a stranger could actually
// connect to. The standard library covers loopback, RFC1918, link-local and
// multicast; the table above covers the rest.
func isShareableIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// An IPv4-mapped v6 address is judged on the embedded v4, or 127.0.0.1
	// dressed as ::ffff:7f00:1 would sail through.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, n := range nonRoutable {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// normalizePeerAddr turns one getpeerinfo `addr` into the wire form the
// collector expects, or "" if it is not worth sending.
//
// The returned form is always host:port with IPv6 bracketed, because an
// unbracketed v6 address and port cannot be parsed back apart.
func normalizePeerAddr(addr string) string {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil || !isShareableIP(ip) {
		return ""
	}
	return net.JoinHostPort(ip.String(), portStr)
}

// shareablePeers picks the peers worth reporting out of a getpeerinfo result.
//
// Outbound only, and that is not an optimisation. For a connection we dialled,
// `addr` IS the peer's listening address, so it is provably reachable and can
// be handed to another wallet as an addnode. For an inbound connection `addr`
// carries the far end's ephemeral source port, which nothing can connect back
// to — sharing those would fill the list with dead entries.
//
// The result is sorted and de-duplicated so an unchanged peer set produces an
// identical payload, which makes the whole thing easier to reason about and
// to test.
func shareablePeers(peers []PeerInfo) []string {
	seen := make(map[string]struct{}, len(peers))
	out := make([]string, 0, len(peers))
	for _, p := range peers {
		if p.Inbound {
			continue
		}
		addr := normalizePeerAddr(p.Addr)
		if addr == "" {
			continue
		}
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	sort.Strings(out)
	if len(out) > maxSharedPeers {
		out = out[:maxSharedPeers]
	}
	return out
}

// scheduleReport decides what one heartbeat beat should do: which slot to book
// next, and whether a report is due right now.
//
// It lives here, with the other pure transforms, because a schedule expressed
// as a timer can only be tested by waiting for one. `booked` is the slot the
// previous beat wrote down, zero meaning nothing is scheduled yet.
//
// A zero slot never sends. That is what keeps the first beat of a run, and the
// first beat after sharing is switched on, from firing immediately: the wallet
// gets a full interval to build a real peer set first.
func scheduleReport(now, booked time.Time, interval time.Duration) (next time.Time, send bool) {
	if interval <= 0 {
		interval = telemetryInterval
	}
	switch {
	case booked.IsZero():
		return now.Add(interval), false
	case now.Before(booked):
		return booked, false
	default:
		return now.Add(interval), true
	}
}

// networkTag is the short network name the collector expects.
func networkTag(testnet bool) string {
	if testnet {
		return "test"
	}
	return "main"
}

// buildReport assembles the payload. It returns ok=false when there is
// nothing worth sending, so callers can skip the request entirely rather than
// posting an empty peer list.
func buildReport(peers []PeerInfo, testnet bool, reporterID string) (peerReport, bool) {
	shared := shareablePeers(peers)
	if len(shared) == 0 || reporterID == "" {
		return peerReport{}, false
	}
	return peerReport{
		Version:  telemetryVersion,
		Reporter: reporterID,
		Client:   "gridcoinresearch-tui/" + version,
		Network:  networkTag(testnet),
		Peers:    shared,
	}, true
}
