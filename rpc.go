// This file is a minimal JSON-RPC 2.0 client over plain HTTP. Gridcoin's
// daemon speaks the same bitcoind dialect, so one thin Call(method, params,
// out) function covers every RPC we need. No third-party RPC library, to
// keep the binary tiny.
//
// JSON-RPC 2.0 request envelope we send:
//
//	{"jsonrpc":"1.0","id":N,"method":"getbalance","params":[]}
//
// JSON-RPC 2.0 response envelope the daemon returns:
//
//	{"result":<whatever>,"error":null,"id":N}
//	{"result":null,"error":{"code":-32601,"message":"Method not found"},"id":N}
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic" // thread-safe counter for unique request IDs
	"time"
)

// RPCClient is the object the rest of the program uses to talk to the daemon.
// Lowercase field names mean "unexported" — they can only be touched from
// within this package, so nothing outside rpc.go can mess with the HTTP client.
type RPCClient struct {
	url        string
	user       string
	password   string
	httpClient *http.Client
	// atomic.Uint64 is a counter that is safe to increment from multiple
	// goroutines at once (Bubble Tea runs each RPC call in its own goroutine
	// via tea.Cmd, so concurrent increments are a real possibility).
	reqID atomic.Uint64
}

// NewRPCClient builds a fresh client from a resolved Config. The returned
// value is a pointer (*RPCClient) so mutation of the counter persists across
// calls and so copying the client doesn't duplicate the HTTP connection pool.
func NewRPCClient(cfg Config) *RPCClient {
	return &RPCClient{
		url:      cfg.URL(),
		user:     cfg.User,
		password: cfg.Password,
		httpClient: &http.Client{
			// 5 minutes is a "don't hang forever" ceiling, not a target.
			// listreceivedbyaddress walks the entire wallet to tally
			// per-address receipts and on a large wallet with many
			// thousands of historical transactions that can legitimately
			// take several minutes. Typical RPCs still return in
			// milliseconds — the timeout is just how long we wait before
			// giving up on a clearly-stuck daemon.
			Timeout: 5 * time.Minute,
			// We fire 4-5 concurrent RPCs per tick to the same host, so
			// keep connections pooled instead of churning TCP.
			Transport: &http.Transport{
				MaxIdleConns:        16,
				MaxIdleConnsPerHost: 8,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// rpcRequest and rpcResponse are the wire format. The `json:"name"` struct
// tags tell encoding/json to use those exact lowercase keys. Go conventions
// use CamelCase field names, but JSON wants lowercase — the tags bridge that.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	// []any is a slice of "anything" — each param can be a number, bool,
	// string, nested struct, etc. encoding/json handles the conversion.
	Params []any `json:"params"`
}

type rpcResponse struct {
	// json.RawMessage is a "hold these bytes as-is, I'll decode them later"
	// type. We can't know the shape of `result` until the caller tells us
	// what struct they want to decode it into, so we defer the parsing.
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	ID     uint64          `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Error makes *rpcError satisfy Go's built-in `error` interface (any type
// with an Error() string method is an error). This lets the caller treat RPC
// errors and transport errors uniformly.
func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Call is the single entry point. Pass the method name, the positional
// params, and a pointer to the struct you want the result decoded into.
// Pass nil for `out` if you don't care about the return value (e.g.
// walletlock only needs to know it succeeded).
func (c *RPCClient) Call(method string, params []any, out any) error {
	if params == nil {
		params = []any{} // the daemon prefers an empty array to null
	}
	// Marshal the request envelope into JSON bytes.
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "1.0",
		ID:      c.reqID.Add(1), // returns new value after atomic increment
		Method:  method,
		Params:  params,
	})
	if err != nil {
		// %w wraps the underlying error so callers can inspect it via errors.Is/As.
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Empty credentials are valid — skip Basic auth entirely rather than sending empty values.
	if c.user != "" && c.password != "" {
		req.SetBasicAuth(c.user, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close() // always close the body when this function returns

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	// gridcoinresearchd returns the JSON-RPC envelope with a non-200 status on RPC
	// errors (e.g. 500 for "method not found"), so trust the envelope first.
	var parsed rpcResponse
	if jsonErr := json.Unmarshal(raw, &parsed); jsonErr != nil {
		// If the body isn't valid JSON, we probably hit a reverse proxy error
		// page or a malformed daemon response. Surface as much of the raw body
		// as is useful for debugging, truncated to keep error logs sane.
		if resp.StatusCode >= 400 {
			body := string(raw)
			if len(body) > 200 {
				body = body[:200] + "…"
			}
			return fmt.Errorf("http %d: %s", resp.StatusCode, body)
		}
		return fmt.Errorf("decode response: %w", jsonErr)
	}
	if parsed.Error != nil {
		return parsed.Error
	}
	// Only decode the result into `out` if the caller asked for it AND the
	// daemon actually returned something (not null / not empty).
	if out != nil && len(parsed.Result) > 0 && string(parsed.Result) != "null" {
		if err := json.Unmarshal(parsed.Result, out); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
	}
	return nil
}
