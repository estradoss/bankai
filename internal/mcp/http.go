package mcp

// Streamable-HTTP transport for MCP. A single endpoint accepts JSON-RPC via
// POST; the server replies with either application/json (one response) or
// text/event-stream (SSE, one or more `data:` events). Session continuity is
// carried by the Mcp-Session-Id header returned from initialize. This is the
// modern replacement for the deprecated HTTP+SSE two-endpoint transport.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpDoer is the subset of *http.Client used here (swappable in tests).
type httpDoer struct{ c *http.Client }

func (h *httpDoer) Do(req *http.Request) (*http.Response, error) { return h.c.Do(req) }

// DialHTTP connects to a streamable-HTTP MCP server and completes the initialize
// handshake. authHeader, if non-empty, is sent as the Authorization header.
func DialHTTP(ctx context.Context, name, url, authHeader string) (*Client, error) {
	c := &Client{
		Name:       name,
		httpURL:    url,
		httpClient: &httpDoer{c: &http.Client{Timeout: 60 * time.Second}},
		authHeader: authHeader,
		done:       make(chan struct{}),
	}
	if err := c.handshake(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) httpNextID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return c.nextID
}

// httpPost sends a JSON-RPC payload and returns the raw response body plus its
// content-type. It also captures the Mcp-Session-Id header on first sight.
func (c *Client) httpPost(ctx context.Context, payload []byte) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.httpURL, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("mcp http %s: %d %s", c.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func (c *Client) httpCall(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.httpNextID()
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	body, ctype, err := c.httpPost(ctx, payload)
	if err != nil {
		return nil, err
	}
	resp, err := extractRPCResponse(body, ctype, id)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

func (c *Client) httpNotify(ctx context.Context, method string, params interface{}) error {
	payload, err := json.Marshal(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	// Notifications get a 202 with no body; ignore the response content.
	_, _, err = c.httpPost(ctx, payload)
	return err
}

// extractRPCResponse pulls the JSON-RPC response matching id from either a plain
// JSON body or an SSE (text/event-stream) body.
func extractRPCResponse(body []byte, contentType string, id int) (*rpcResponse, error) {
	if strings.Contains(contentType, "text/event-stream") {
		return parseSSE(body, id)
	}
	// Plain JSON: a single response object (ignore batch for simplicity).
	var r rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(body), &r); err != nil {
		return nil, fmt.Errorf("decode mcp response: %w", err)
	}
	return &r, nil
}

// parseSSE scans an event stream for the `data:` line whose JSON-RPC response id
// matches the request. Returns the first response if no id matches (some servers
// omit ids on single-response streams).
func parseSSE(body []byte, id int) (*rpcResponse, error) {
	scan := bufio.NewScanner(bytes.NewReader(body))
	scan.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var first *rpcResponse
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			continue
		}
		if r.ID == id {
			return &r, nil
		}
		if first == nil {
			cp := r
			first = &cp
		}
	}
	if first != nil {
		return first, nil
	}
	return nil, fmt.Errorf("no JSON-RPC response found in event stream")
}
