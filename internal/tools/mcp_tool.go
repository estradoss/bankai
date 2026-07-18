package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/estradoss/bankai/internal/mcp"
)

// MCPTool adapts a bridged MCP server tool to the Tool interface so the engine
// can dispatch to it like any native tool. Its name is namespaced
// (mcp__<server>__<tool>) to avoid collisions with built-in tools.
type MCPTool struct {
	Bridged mcp.BridgedTool
}

func (t MCPTool) Name() string { return t.Bridged.QualifiedName }

func (t MCPTool) Description() string {
	d := t.Bridged.Info.Description
	if d == "" {
		d = "MCP tool " + t.Bridged.Info.Name + " from server " + t.Bridged.Server
	}
	return "(MCP: " + t.Bridged.Server + ") " + d
}

func (t MCPTool) InputSchema() json.RawMessage {
	if len(t.Bridged.Info.InputSchema) > 0 {
		return t.Bridged.Info.InputSchema
	}
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t MCPTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	out, isErr, err := t.Bridged.Call(ctx, input)
	if err != nil {
		return Result{IsError: true, Output: "mcp call failed: " + err.Error()}, nil
	}
	if out == "" {
		out = "(no content)"
	}
	return Result{Output: out, IsError: isErr}, nil
}

// RegisterMCPTools wraps and registers every bridged MCP tool in reg.
func RegisterMCPTools(reg *Registry, bridged []mcp.BridgedTool) {
	for _, b := range bridged {
		reg.Register(MCPTool{Bridged: b})
	}
}

// ListMcpResourcesTool lists resources advertised by connected MCP servers.
type ListMcpResourcesTool struct{ Mgr *mcp.Manager }

func (ListMcpResourcesTool) Name() string { return "ListMcpResources" }
func (ListMcpResourcesTool) Description() string {
	return "List resources exposed by connected MCP servers (uri, name, description). Read one with ReadMcpResource."
}
func (ListMcpResourcesTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t ListMcpResourcesTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Mgr == nil {
		return Result{Output: "no MCP servers connected"}, nil
	}
	res := t.Mgr.Resources()
	if len(res) == 0 {
		return Result{Output: "no MCP resources available"}, nil
	}
	var b strings.Builder
	for i, r := range res {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s  (%s) %s — %s", r.Info.URI, r.Server, r.Info.Name, r.Info.Description)
	}
	return Result{Output: b.String()}, nil
}

// ReadMcpResourceTool reads a resource by URI from an MCP server.
type ReadMcpResourceTool struct{ Mgr *mcp.Manager }

func (ReadMcpResourceTool) Name() string { return "ReadMcpResource" }
func (ReadMcpResourceTool) Description() string {
	return "Read the contents of an MCP resource by its uri (from ListMcpResources)."
}
func (ReadMcpResourceTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{"uri":{"type":"string","description":"Resource URI from ListMcpResources"}},
		"required":["uri"]
	}`)
}
func (t ReadMcpResourceTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Mgr == nil {
		return Result{IsError: true, Output: "no MCP servers connected"}, nil
	}
	var in struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.URI == "" {
		return Result{IsError: true, Output: "uri is required"}, nil
	}
	out, found, err := t.Mgr.ReadResource(ctx, in.URI)
	if err != nil {
		return Result{IsError: true, Output: "read failed: " + err.Error()}, nil
	}
	if !found {
		return Result{IsError: true, Output: "unknown resource uri: " + in.URI}, nil
	}
	if out == "" {
		out = "(empty resource)"
	}
	return Result{Output: out}, nil
}
