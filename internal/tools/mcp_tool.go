package tools

import (
	"context"
	"encoding/json"

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
