// The side-effecting half of peer sharing: posting a report to the collector.
//
// Split from telemetry.go the same way selfupdate.go is split from
// updatecheck.go — the transforms stay pure and testable, the I/O lives here.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// telemetryEndpoint is where reports go. addnodes.gridcoin.club serves the
	// peer list itself; /v1/reports is the only path on it backed by an
	// application.
	telemetryEndpoint = "https://addnodes.gridcoin.club/v1/reports"

	// telemetryHTTPTimeout bounds the post. Short on purpose, for the same
	// reason the update check is: a slow or blocked collector must never make
	// the TUI feel wedged. Sharing peers is a favour to the network, not
	// something the user is waiting on.
	telemetryHTTPTimeout = 20 * time.Second

	// telemetryInterval is the default gap between reports. The server can
	// override it per-response via next_report_after.
	telemetryInterval = time.Hour

	// Bounds on what the server may ask for, so a bad or hostile value cannot
	// turn the client into a hot loop or silence it for a month.
	telemetryMinInterval = 15 * time.Minute
	telemetryMaxInterval = 24 * time.Hour

	// sharingHeartbeat is how often the TUI checks whether a report is due. It
	// is NOT the report cadence, just the granularity of the check, so a
	// report can be at most one beat late. Keeping the timer fixed and the
	// cadence in the model is what lets a new next_report_after take effect on
	// the next report rather than the one after it.
	sharingHeartbeat = time.Minute
)

// telemetryURL returns the collector endpoint. GRC_PEER_SHARING_URL overrides
// it for local development and tests; it is deliberately undocumented in the
// README because it is not something a user should need.
func telemetryURL() string {
	if v := strings.TrimSpace(os.Getenv("GRC_PEER_SHARING_URL")); v != "" {
		return v
	}
	return telemetryEndpoint
}

// clampReportInterval keeps a server-suggested cadence inside sane bounds.
func clampReportInterval(seconds int) time.Duration {
	if seconds <= 0 {
		return telemetryInterval
	}
	d := time.Duration(seconds) * time.Second
	if d < telemetryMinInterval {
		return telemetryMinInterval
	}
	if d > telemetryMaxInterval {
		return telemetryMaxInterval
	}
	return d
}

// postReport sends one report. endpoint is a parameter rather than a constant
// so tests can point it at an httptest server and never touch the network,
// exactly as fetchLatestRelease takes its base URL.
func postReport(endpoint string, report peerReport) (reportAck, error) {
	body, err := json.Marshal(report)
	if err != nil {
		return reportAck{}, err
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return reportAck{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gridcoinresearch-tui/"+version)

	resp, err := (&http.Client{Timeout: telemetryHTTPTimeout}).Do(req)
	if err != nil {
		return reportAck{}, err
	}
	defer resp.Body.Close()

	// Read through a LimitReader: the collector is ours, but a proxy or
	// captive portal in between is not, and an unbounded read of whatever
	// answered would be careless.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return reportAck{}, err
	}

	var ack reportAck
	// A non-JSON body means something other than the collector replied.
	// Ignore the decode error and let the status decide.
	_ = json.Unmarshal(raw, &ack)

	if resp.StatusCode != http.StatusOK {
		return ack, fmt.Errorf("collector returned %s", resp.Status)
	}
	return ack, nil
}
