package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMain doubles as a mock MCP server when BANKAI_MCP_HELPER=1: it speaks
// newline-delimited JSON-RPC 2.0 on stdin/stdout, implementing initialize,
// tools/list, and tools/call.
func TestMain(m *testing.M) {
	if os.Getenv("BANKAI_MCP_HELPER") == "1" {
		runMockServer()
		return
	}
	os.Exit(m.Run())
}

func runMockServer() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1<<20)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil {
			continue
		}
		if req.ID == 0 {
			continue // notification
		}
		var result interface{}
		switch req.Method {
		case "initialize":
			result = map[string]interface{}{"protocolVersion": protocolVersion}
		case "tools/list":
			result = map[string]interface{}{"tools": []map[string]interface{}{
				{"name": "echo", "description": "echoes text", "inputSchema": map[string]interface{}{"type": "object"}},
			}}
		case "tools/call":
			var p struct {
				Name      string                 `json:"name"`
				Arguments map[string]interface{} `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			result = map[string]interface{}{"content": []map[string]interface{}{
				{"type": "text", "text": fmt.Sprintf("echo:%v", p.Arguments["text"])},
			}}
		default:
			result = map[string]interface{}{}
		}
		_ = out.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
}

func dialMock(t *testing.T) *Client {
	t.Helper()
	c, err := Dial(context.Background(), "mock", os.Args[0], nil, append(os.Environ(), "BANKAI_MCP_HELPER=1"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

func TestHandshakeAndListTools(t *testing.T) {
	c := dialMock(t)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestCallTool(t *testing.T) {
	c := dialMock(t)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, isErr, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || isErr {
		t.Fatalf("call: out=%q isErr=%v err=%v", out, isErr, err)
	}
	if out != "echo:hi" {
		t.Fatalf("out = %q", out)
	}
}

func TestStartBridgesTools(t *testing.T) {
	cfgs := map[string]ServerConfig{
		"mock": {Command: os.Args[0], Env: map[string]string{"BANKAI_MCP_HELPER": "1"}},
		"sse":  {Type: "sse", Command: "x"}, // skipped: unsupported transport
	}
	mgr, bridged, errs := Start(context.Background(), cfgs)
	defer mgr.Close()
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(bridged) != 1 || bridged[0].QualifiedName != "mcp__mock__echo" {
		t.Fatalf("bridged = %+v", bridged)
	}
}
