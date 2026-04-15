// Unit tests. A quick Go testing primer for the unfamiliar:
//
//   • Test functions must start with "Test" and take *testing.T.
//   • `go test ./...` discovers and runs every Test* function.
//   • t.Errorf records a failure and keeps running; t.Fatal stops this test.
//   • t.Run(name, func) creates a "sub-test" so table-driven tests get
//     individual pass/fail lines in the output.
//   • t.Setenv sets an env var for the duration of the test and restores
//     it afterwards — safer than poking os.Setenv directly.
//   • t.TempDir() gives a unique scratch directory that is auto-deleted
//     when the test ends.
//   • httptest.NewServer spins up a real HTTP server on a random port that
//     the test can point its client at, so we don't need a real daemon.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRPCClientCallSuccess spins up a fake JSON-RPC server, points the
// client at it, and checks that:
//   (a) we send the right method name,
//   (b) we send a Basic auth header when credentials are present,
//   (c) we correctly decode the result into the caller's struct.
func TestRPCClientCallSuccess(t *testing.T) {
	var gotMethod string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var req rpcRequest
		_ = json.Unmarshal(body, &req)
		gotMethod = req.Method
		_, _ = w.Write([]byte(`{"result":{"balance":42.5},"error":null,"id":1}`))
	}))
	defer srv.Close()

	c := &RPCClient{url: srv.URL, user: "u", password: "p", httpClient: srv.Client()}
	var out WalletInfo
	if err := c.Call("getwalletinfo", nil, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if gotMethod != "getwalletinfo" {
		t.Errorf("method = %q, want getwalletinfo", gotMethod)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("auth header = %q, want Basic ...", gotAuth)
	}
	if out.Balance != 42.5 {
		t.Errorf("balance = %v, want 42.5", out.Balance)
	}
}

// TestRPCClientNoAuthHeaderWhenEmpty asserts the "empty credentials means
// no auth header" rule. The load-bearing line in rpc.go is
// `if c.user != "" && c.password != "" { req.SetBasicAuth(...) }` — this
// test ensures we never accidentally start sending an empty Basic header.
func TestRPCClientNoAuthHeaderWhenEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"result":null,"error":null,"id":1}`))
	}))
	defer srv.Close()

	c := &RPCClient{url: srv.URL, httpClient: srv.Client()} // empty credentials
	if err := c.Call("getwalletinfo", nil, nil); err != nil {
		t.Fatalf("call: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("auth header should be empty, got %q", gotAuth)
	}
}

// TestRPCClientRPCError drives the "daemon returned an error envelope"
// path: HTTP 500 plus a JSON-RPC error body. We should see the error
// message bubble up to the caller intact.
func TestRPCClientRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"result":null,"error":{"code":-32601,"message":"method not found"},"id":1}`))
	}))
	defer srv.Close()

	c := &RPCClient{url: srv.URL, httpClient: srv.Client()}
	err := c.Call("nosuchmethod", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "method not found") {
		t.Errorf("error = %v, want 'method not found'", err)
	}
}

// TestClassifyTransaction is a table-driven test: each row is one scenario,
// we run them all through ClassifyTransaction and compare the enum output.
// Classifying on a typed enum (TxStatusKind) rather than a raw string means
// a typo in the comparison would fail to compile — much safer than string
// equality.
func TestClassifyTransaction(t *testing.T) {
	cases := []struct {
		name string
		tx   Transaction
		kind TxStatusKind
	}{
		{"pending receive", Transaction{Category: "receive", Confirmations: 0}, TxStatusUpcoming},
		{"pending send", Transaction{Category: "send", Confirmations: 0}, TxStatusUpcoming},
		{"shallow receive", Transaction{Category: "receive", Confirmations: 2}, TxStatusIncoming},
		{"shallow send", Transaction{Category: "send", Confirmations: 2}, TxStatusSending},
		{"deep receive", Transaction{Category: "receive", Confirmations: 100}, TxStatusConfirmed},
		{"deep send", Transaction{Category: "send", Confirmations: 100}, TxStatusConfirmed},
		{"stake", Transaction{Category: "generate", Confirmations: 50}, TxStatusStake},
		{"immature stake", Transaction{Category: "immature", Confirmations: 3}, TxStatusStake},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyTransaction(tc.tx).Kind; got != tc.kind {
				t.Errorf("got %v, want %v", got, tc.kind)
			}
		})
	}
}

// TestReadConfFile ensures the conf parser picks up the keys we care about
// and silently skips the ones we don't (server=, rpcallowip=).
func TestReadConfFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gridcoinresearch.conf")
	content := `# comment line
rpcuser=alice
rpcpassword=s3cret
rpcport=15715
server=1
rpcallowip=127.0.0.1
rpcconnect=192.168.1.10
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	v := readConfFile(path)
	if v == nil {
		t.Fatal("expected non-nil")
	}
	if v.rpcuser != "alice" || v.rpcpassword != "s3cret" || v.rpcport != "15715" || v.rpcconnect != "192.168.1.10" {
		t.Errorf("parsed = %+v", v)
	}
}

// TestReadConfFileMissing locks in the "missing conf is not an error" rule.
// Both a nonexistent path and an empty path must return nil (signalling
// "no values found") rather than crashing.
func TestReadConfFileMissing(t *testing.T) {
	if v := readConfFile("/nonexistent/path/nope.conf"); v != nil {
		t.Errorf("expected nil for missing file, got %+v", v)
	}
	if v := readConfFile(""); v != nil {
		t.Errorf("expected nil for empty path, got %+v", v)
	}
}

// TestLoadConfigDefaults checks that running with no flags and an empty
// HOME yields the built-in fallback values (mainnet port, localhost, empty
// credentials). t.Setenv("HOME", t.TempDir()) makes sure we don't read the
// developer's real conf file.
func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GRC_RPC_HOST", "")
	t.Setenv("GRC_RPC_PORT", "")
	t.Setenv("GRC_RPC_USER", "")
	t.Setenv("GRC_RPC_PASSWORD", "")

	cfg, err := LoadConfig([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("host = %q, want 127.0.0.1", cfg.Host)
	}
	if cfg.Port != "15715" {
		t.Errorf("port = %q, want 15715 (mainnet default)", cfg.Port)
	}
	if cfg.User != "" || cfg.Password != "" {
		t.Errorf("expected empty credentials, got user=%q pass=%q", cfg.User, cfg.Password)
	}
	if cfg.NetworkName != "mainnet" {
		t.Errorf("network = %q", cfg.NetworkName)
	}
}

// TestLoadConfigTestnetDefaults checks that --testnet flips the default port.
func TestLoadConfigTestnetDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GRC_RPC_PORT", "")
	cfg, err := LoadConfig([]string{"--testnet"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "25715" {
		t.Errorf("testnet port = %q, want 25715", cfg.Port)
	}
}

// TestLoadConfigFlagOverridesEnv pins the cascade order: an explicit flag
// must win over an environment variable that would otherwise apply.
func TestLoadConfigFlagOverridesEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GRC_RPC_HOST", "env.example")
	cfg, err := LoadConfig([]string{"--rpc-host", "flag.example"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "flag.example" {
		t.Errorf("host = %q, want flag.example (flag wins)", cfg.Host)
	}
}

// TestFormatGRC sanity-checks the short-form amount humaniser: sign prefix
// for nonzero, plain "0.00" for zero, thousands separator for big numbers.
func TestFormatGRC(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0.00 GRC"},
		{12.34, "+12.34 GRC"},
		{-100.00, "−100.00 GRC"},
		{12345.67, "+12,345.67 GRC"},
	}
	for _, tc := range cases {
		if got := FormatGRC(tc.in); got != tc.want {
			t.Errorf("FormatGRC(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
