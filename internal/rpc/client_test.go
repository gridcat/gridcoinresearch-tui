package rpc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRPCClientCallSuccess spins up a fake JSON-RPC server, points the
// client at it, and checks that:
//
//	(a) we send the right method name,
//	(b) we send a Basic auth header when credentials are present,
//	(c) we correctly decode the result into the caller's struct.
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

	c := &Client{url: srv.URL, user: "u", password: "p", httpClient: srv.Client()}
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
// no auth header" rule. The load-bearing line in client.go is
// `if c.user != "" && c.password != "" { req.SetBasicAuth(...) }`, and this
// test ensures we never accidentally start sending an empty Basic header.
func TestRPCClientNoAuthHeaderWhenEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"result":null,"error":null,"id":1}`))
	}))
	defer srv.Close()

	c := &Client{url: srv.URL, httpClient: srv.Client()} // empty credentials
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

	c := &Client{url: srv.URL, httpClient: srv.Client()}
	err := c.Call("nosuchmethod", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "method not found") {
		t.Errorf("error = %v, want 'method not found'", err)
	}
}
