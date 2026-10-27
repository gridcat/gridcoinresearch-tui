package rpc

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// hangingDaemon takes every request and answers only once release is closed,
// the way a deadlocked gridcoinresearchd never does.
func hangingDaemon(t *testing.T) (*httptest.Server, chan struct{}) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
			_, _ = w.Write([]byte(`{"result":{},"error":null,"id":1}`))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	return srv, release
}

// waitSent polls until a call shows up as sent, or fails the test.
func waitSent(t *testing.T, c *Client) ([]Pending, time.Time) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		calls, quiet := c.Pending()
		if len(calls) == 1 && calls[0].Sent {
			return calls, quiet
		}
		if time.Now().After(deadline) {
			t.Fatalf("call never showed up as sent, last = %+v", calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// While a call waits on a silent daemon it is listed as sent and the quiet
// starts with it; once the reply lands nothing is pending.
func TestPendingWhileDaemonHangs(t *testing.T) {
	srv, release := hangingDaemon(t)
	c := &Client{url: srv.URL, httpClient: srv.Client()}
	if calls, _ := c.Pending(); calls != nil {
		t.Fatal("pending before any call")
	}

	errc := make(chan error, 1)
	start := time.Now()
	go func() { errc <- c.Call("getwalletinfo", nil, nil) }()

	calls, quiet := waitSent(t, c)
	if calls[0].Method != "getwalletinfo" {
		t.Errorf("method = %q, want getwalletinfo", calls[0].Method)
	}
	if quiet.Before(start) || !quiet.Equal(calls[0].Since) {
		t.Errorf("quiet since %v, want the call's start %v", quiet, calls[0].Since)
	}

	close(release)
	if err := <-errc; err != nil {
		t.Fatalf("call: %v", err)
	}
	if calls, quiet := c.Pending(); calls != nil || !quiet.IsZero() {
		t.Errorf("still pending after the reply: %+v since %v", calls, quiet)
	}
}

// A call that times out unanswered must not restart the count: the next call
// keeps the quiet that began with the first. Only a reply ends it.
func TestTimeoutKeepsTheQuiet(t *testing.T) {
	srv, release := hangingDaemon(t)
	hc := srv.Client()
	hc.Timeout = 50 * time.Millisecond
	c := &Client{url: srv.URL, httpClient: hc}

	firstStart := time.Now()
	if err := c.Call("getwalletinfo", nil, nil); err == nil {
		t.Fatal("first call should have timed out")
	}

	hc.Timeout = 5 * time.Second
	errc := make(chan error, 1)
	go func() { errc <- c.Call("getblockchaininfo", nil, nil) }()
	calls, quiet := waitSent(t, c)
	if quiet.Before(firstStart) || !quiet.Before(calls[0].Since) {
		t.Errorf("quiet since %v, want the timed-out call's start (after %v, before %v)", quiet, firstStart, calls[0].Since)
	}

	close(release)
	if err := <-errc; err != nil {
		t.Fatalf("second call: %v", err)
	}
	_, done := c.track(99, "getpeerinfo")
	defer done(true)
	if calls, quiet := c.Pending(); !quiet.Equal(calls[0].Since) {
		t.Errorf("after a reply the quiet should start fresh, got %v want %v", quiet, calls[0].Since)
	}
}

// A refused connection fails fast and says the daemon isn't there; it is not
// silence, so it ends the quiet like a reply does.
func TestRefusedConnectionEndsTheQuiet(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	c := &Client{url: "http://" + addr, httpClient: &http.Client{Timeout: 5 * time.Second}}
	if err := c.Call("getwalletinfo", nil, nil); err == nil {
		t.Fatal("call to a closed port should fail")
	}
	_, done := c.track(1, "getwalletinfo")
	defer done(true)
	if calls, quiet := c.Pending(); !quiet.Equal(calls[0].Since) {
		t.Errorf("refusal left an old quiet: %v, want %v", quiet, calls[0].Since)
	}
}

// Ordering and bookkeeping without a server.
func TestPendingTracksEveryCall(t *testing.T) {
	c := &Client{}
	setSent, doneFirst := c.track(1, "getblockchaininfo")
	time.Sleep(time.Millisecond)
	_, doneSecond := c.track(2, "getpeerinfo")

	calls, quiet := c.Pending()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want 2", calls)
	}
	first := calls[0]
	if first.Method != "getblockchaininfo" {
		first = calls[1]
	}
	if !quiet.Equal(first.Since) {
		t.Errorf("quiet since %v, want the first call's start %v", quiet, first.Since)
	}

	setSent(true)
	setSent(false) // a retry on a fresh connection
	for _, p := range func() []Pending { cs, _ := c.Pending(); return cs }() {
		if p.Sent {
			t.Errorf("%s still marked sent after a re-dial", p.Method)
		}
	}

	doneFirst(true)
	calls, quiet = c.Pending()
	if len(calls) != 1 || calls[0].Method != "getpeerinfo" || !quiet.Equal(calls[0].Since) {
		t.Errorf("after the first reply: %+v since %v", calls, quiet)
	}
	doneSecond(true)
	if calls, _ := c.Pending(); calls != nil {
		t.Error("pending after both calls finished")
	}
}
