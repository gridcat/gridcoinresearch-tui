package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The peer-sharing tests. The rule these all exist to protect is simple: what
// leaves the machine is the addresses of peers we dialled out to, and nothing
// else. Everything below is a way of checking one edge of that.

func TestShareablePeersDropsInbound(t *testing.T) {
	// An inbound peer's `addr` carries the far end's ephemeral source port,
	// which nothing can connect back to. Sharing those would fill the public
	// list with dead entries.
	peers := []PeerInfo{
		{Addr: "45.33.32.156:32749", Inbound: false},
		{Addr: "93.184.216.34:51234", Inbound: true},
	}
	got := shareablePeers(peers)
	if len(got) != 1 || got[0] != "45.33.32.156:32749" {
		t.Fatalf("expected only the outbound peer, got %v", got)
	}
}

func TestShareablePeersRejectsNonRoutable(t *testing.T) {
	// The table that matters. Every one of these would either point back at
	// the reporter's own network or at documentation space.
	for _, addr := range []string{
		"127.0.0.1:32749",          // loopback
		"10.1.2.3:32749",           // RFC1918
		"192.168.1.5:32749",        // RFC1918
		"172.16.0.1:32749",         // RFC1918
		"169.254.1.1:32749",        // link-local
		"100.64.0.1:32749",         // CGNAT
		"224.0.0.1:32749",          // multicast
		"240.0.0.1:32749",          // reserved
		"0.0.0.0:32749",            // unspecified
		"192.0.2.1:32749",          // TEST-NET-1
		"198.51.100.1:32749",       // TEST-NET-2
		"203.0.113.1:32749",        // TEST-NET-3
		"198.18.0.1:32749",         // benchmarking
		"[::1]:32749",              // v6 loopback
		"[fe80::1]:32749",          // v6 link-local
		"[fc00::1]:32749",          // v6 unique-local
		"[2001:db8::1]:32749",      // v6 documentation
		"[::ffff:127.0.0.1]:32749", // loopback wearing a v6 costume
		"[::ffff:10.0.0.1]:32749",  // RFC1918 likewise
	} {
		if out := normalizePeerAddr(addr); out != "" {
			t.Errorf("expected %s to be rejected, got %q", addr, out)
		}
	}
}

func TestShareablePeersAcceptsRoutable(t *testing.T) {
	for _, addr := range []string{
		"45.33.32.156:32749",
		"8.8.8.8:32748",
		"172.32.0.1:32749", // just outside RFC1918
		"[2606:4700:4700::1111]:32749",
	} {
		if out := normalizePeerAddr(addr); out == "" {
			t.Errorf("expected %s to be accepted", addr)
		}
	}
}

func TestNormalizePeerAddrBracketsIPv6(t *testing.T) {
	// The collector re-parses these strings. An unbracketed v6 address and
	// port cannot be split apart again, so every v6 peer would be silently
	// dropped on the far side.
	got := normalizePeerAddr("[2606:4700:4700::1111]:32749")
	if got != "[2606:4700:4700::1111]:32749" {
		t.Fatalf("v6 address lost its brackets: %q", got)
	}
}

func TestNormalizePeerAddrRejectsJunk(t *testing.T) {
	for _, addr := range []string{
		"", "not-an-address", "45.33.32.156", // no port
		"45.33.32.156:0", "45.33.32.156:70000", // port out of range
		"example.net:32749", // a hostname is not something getpeerinfo returns
	} {
		if out := normalizePeerAddr(addr); out != "" {
			t.Errorf("expected %q to be rejected, got %q", addr, out)
		}
	}
}

func TestShareablePeersDeduplicatesAndSorts(t *testing.T) {
	peers := []PeerInfo{
		{Addr: "8.8.8.8:32749"},
		{Addr: "45.33.32.156:32749"},
		{Addr: "8.8.8.8:32749"},
	}
	got := shareablePeers(peers)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique peers, got %v", got)
	}
	if got[0] != "45.33.32.156:32749" || got[1] != "8.8.8.8:32749" {
		t.Fatalf("expected sorted output, got %v", got)
	}
}

func TestShareablePeersCaps(t *testing.T) {
	peers := make([]PeerInfo, 0, maxSharedPeers+10)
	for i := 0; i < maxSharedPeers+10; i++ {
		peers = append(peers, PeerInfo{Addr: "45.33." + itoa(i/256) + "." + itoa(i%256) + ":32749"})
	}
	if got := shareablePeers(peers); len(got) != maxSharedPeers {
		t.Fatalf("expected the list capped at %d, got %d", maxSharedPeers, len(got))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestBuildReportShape(t *testing.T) {
	report, ok := buildReport([]PeerInfo{{Addr: "45.33.32.156:32749"}}, false, "a1b2c3d4e5f60718293a4b5c6d7e8f90")
	if !ok {
		t.Fatal("expected a report")
	}
	if report.Version != telemetryVersion {
		t.Errorf("version = %d", report.Version)
	}
	if report.Network != "main" {
		t.Errorf("network = %q", report.Network)
	}
	if report.Reporter != "a1b2c3d4e5f60718293a4b5c6d7e8f90" {
		t.Errorf("reporter = %q", report.Reporter)
	}

	// The payload must carry these keys and no others. This is the promise the
	// consent screen makes, expressed as a test so widening it breaks here.
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"v": true, "reporter": true, "client": true, "network": true, "peers": true}
	for k := range fields {
		if !want[k] {
			t.Errorf("payload carries unexpected field %q — the consent screen promises addresses only", k)
		}
	}
	for k := range want {
		if _, ok := fields[k]; !ok {
			t.Errorf("payload is missing %q", k)
		}
	}
}

func TestBuildReportTestnetTag(t *testing.T) {
	report, ok := buildReport([]PeerInfo{{Addr: "45.33.32.156:32748"}}, true, "a1b2c3d4e5f60718293a4b5c6d7e8f90")
	if !ok || report.Network != "test" {
		t.Fatalf("expected the testnet tag, got %q (ok=%v)", report.Network, ok)
	}
}

func TestBuildReportSkipsWhenNothingToSend(t *testing.T) {
	// No outbound peers, so there is no request worth making.
	if _, ok := buildReport([]PeerInfo{{Addr: "8.8.8.8:32749", Inbound: true}}, false, "id"); ok {
		t.Error("expected no report when every peer is inbound")
	}
	// No reporter id means consent was never recorded; sending would be wrong.
	if _, ok := buildReport([]PeerInfo{{Addr: "8.8.8.8:32749"}}, false, ""); ok {
		t.Error("expected no report without a reporter id")
	}
}

func TestPostReportSuccess(t *testing.T) {
	var gotBody peerReport
	var gotUA, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"accepted":2,"rejected":1,"next_report_after":1800}`))
	}))
	defer srv.Close()

	report, _ := buildReport([]PeerInfo{
		{Addr: "45.33.32.156:32749"}, {Addr: "8.8.8.8:32749"},
	}, false, "a1b2c3d4e5f60718293a4b5c6d7e8f90")

	ack, err := postReport(srv.URL, report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ack.OK || ack.Accepted != 2 || ack.NextReportAfter != 1800 {
		t.Errorf("unexpected ack: %+v", ack)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotUA == "" {
		t.Error("expected a User-Agent so the collector can tell clients apart")
	}
	if len(gotBody.Peers) != 2 {
		t.Errorf("server saw %d peers", len(gotBody.Peers))
	}
}

func TestPostReportErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error":"rate_limited"}`))
	}))
	defer srv.Close()

	if _, err := postReport(srv.URL, peerReport{}); err == nil {
		t.Fatal("expected an error for a 429")
	}
}

func TestPostReportSurvivesNonJSONBody(t *testing.T) {
	// A captive portal or proxy answering instead of the collector must not
	// panic or hang the client.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>hello</html>"))
	}))
	defer srv.Close()

	if _, err := postReport(srv.URL, peerReport{}); err != nil {
		t.Fatalf("a 200 with a junk body should not error: %v", err)
	}
}

func TestClampReportInterval(t *testing.T) {
	if got := clampReportInterval(0); got != telemetryInterval {
		t.Errorf("zero should fall back to the default, got %v", got)
	}
	if got := clampReportInterval(60); got != telemetryMinInterval {
		t.Errorf("a too-eager server should be clamped up, got %v", got)
	}
	if got := clampReportInterval(999999); got != telemetryMaxInterval {
		t.Errorf("a silencing value should be clamped down, got %v", got)
	}
	if got := clampReportInterval(1800); got != 30*time.Minute {
		t.Errorf("a sane value should pass through, got %v", got)
	}
}

func TestPeerInfoDecodesAddrAndInbound(t *testing.T) {
	// The fields the TUI actually reads, straight off a getpeerinfo shape.
	const payload = `[{"id":1,"addr":"45.33.32.156:32749","inbound":false,"subver":"/Gridcoin:5.5.0.0/"}]`
	var peers []PeerInfo
	if err := json.Unmarshal([]byte(payload), &peers); err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].Addr != "45.33.32.156:32749" || peers[0].Inbound {
		t.Errorf("decoded %+v", peers[0])
	}
}
