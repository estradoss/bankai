// Package mcp is a minimal Model Context Protocol client. It speaks JSON-RPC 2.0
// over a stdio transport (newline-delimited messages) to a locally-spawned MCP
// server, performs the initialize handshake, lists the server's tools, and
// invokes them. It is a focused Go port of vibelearn's MCP subsystem
// (src/services/mcp/) covering the stdio transport and tool bridging; SSE/HTTP
// transports and OAuth are not yet ported.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const protocolVersion = "2024-11-05"

// ToolInfo describes a tool advertised by an MCP server.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Client is a running connection to one stdio MCP server.
type Client struct {
	Name string // logical server name (from config key)

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	enc    *json.Encoder
	mu     sync.Mutex
	nextID int

	pendingMu sync.Mutex
	pending   map[int]chan rpcResponse

	closeOnce sync.Once
	done      chan struct{}
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message) }

// Dial spawns the server command and completes the MCP initialize handshake.
func Dial(ctx context.Context, name, command string, args []string, env []string) (*Client, error) {
	cmd := exec.Command(command, args...)
	if env != nil {
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil // discard server logs
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command, err)
	}
	c := &Client{
		Name:    name,
		cmd:     cmd,
		stdin:   stdin,
		enc:     json.NewEncoder(stdin),
		pending: map[int]chan rpcResponse{},
		done:    make(chan struct{}),
	}
	go c.readLoop(stdout)

	if err := c.handshake(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) readLoop(r io.Reader) {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.ID == 0 {
			continue // notification from server; ignored
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.pendingMu.Unlock()
		if ok {
			ch <- resp
		}
	}
	close(c.done)
}

// call sends a request and waits for its matching response.
func (c *Client) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	err := c.enc.Encode(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("mcp server %s exited", c.Name)
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

func (c *Client) notify(method string, params interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *Client) handshake(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := c.call(cctx, "initialize", map[string]interface{}{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "bankai", "version": "0.1.0"},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	return c.notify("notifications/initialized", nil)
}

// ListTools returns the server's advertised tools.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := c.call(cctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// CallTool invokes a tool and returns the concatenated text of its content
// blocks (the common MCP result shape).
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (string, bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	raw, err := c.call(cctx, "tools/call", map[string]interface{}{
		"name":      name,
		"arguments": json.RawMessage(arguments),
	})
	if err != nil {
		return "", false, err
	}
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return string(raw), false, nil // return raw if not the standard shape
	}
	var b []byte
	for i, blk := range res.Content {
		if i > 0 {
			b = append(b, '\n')
		}
		b = append(b, blk.Text...)
	}
	return string(b), res.IsError, nil
}

// ResourceInfo describes a resource advertised by an MCP server.
type ResourceInfo struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MIMEType    string `json:"mimeType"`
}

// ListResources returns the server's advertised resources. A server without
// resource support may return an error; callers treat that as "none".
func (c *Client) ListResources(ctx context.Context) ([]ResourceInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := c.call(cctx, "resources/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Resources []ResourceInfo `json:"resources"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Resources, nil
}

// ReadResource reads a resource by URI, returning the concatenated text of its
// content blocks.
func (c *Client) ReadResource(ctx context.Context, uri string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	raw, err := c.call(cctx, "resources/read", map[string]interface{}{"uri": uri})
	if err != nil {
		return "", err
	}
	var res struct {
		Contents []struct {
			Text string `json:"text"`
			Blob string `json:"blob"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return string(raw), nil
	}
	var b strings.Builder
	for i, ct := range res.Contents {
		if i > 0 {
			b.WriteByte('\n')
		}
		if ct.Text != "" {
			b.WriteString(ct.Text)
		} else if ct.Blob != "" {
			b.WriteString("(binary blob, " + fmt.Sprint(len(ct.Blob)) + " base64 bytes)")
		}
	}
	return b.String(), nil
}

// Close terminates the server process.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}
