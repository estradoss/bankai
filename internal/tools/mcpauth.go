package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/estradoss/bankai/internal/mcp"
)

// McpAuthTool is the Go port of vibelearn's McpAuthTool. When an HTTP/SSE MCP
// server is configured with `auth: "oauth"` but not yet authenticated, this
// tool starts the OAuth flow on the user's behalf. bankai's OAuth authorizer
// (BrowserAuthorizer) opens the user's browser and runs a loopback callback,
// so a successful call means the token was obtained; the server's tools become
// available the next time the MCP manager dials (or restart). Unlike the TS
// build there is no in-process hot-swap of the tool list — that is the one
// documented deviation.
type McpAuthTool struct {
	// Configs is the resolved MCP server config set (same map passed to
	// mcp.Start). Used to look up the named server's transport + URL.
	Configs map[string]mcp.ServerConfig
}

func (McpAuthTool) Name() string { return "McpAuth" }

func (McpAuthTool) Description() string {
	return "Authenticate an MCP server that requires OAuth. Pass `server` = the server name. Opens the user's browser to authorize; on success the server's tools become available on the next connection. Call with no server to list servers that need authentication."
}

func (McpAuthTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"server": {"type": "string", "description": "Name of the MCP server to authenticate (omit to list servers needing auth)"}
		}
	}`)
}

func (t McpAuthTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Server string `json:"server"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
		}
	}
	if len(t.Configs) == 0 {
		return Result{Output: "No MCP servers are configured."}, nil
	}

	if in.Server == "" {
		var names []string
		for name, cfg := range t.Configs {
			if cfg.Auth == "oauth" {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return Result{Output: "No configured MCP servers use OAuth."}, nil
		}
		sort.Strings(names)
		return Result{Output: "MCP servers configured for OAuth: " + strings.Join(names, ", ") + "\nCall McpAuth with server=<name> to authenticate."}, nil
	}

	cfg, ok := t.Configs[in.Server]
	if !ok {
		return Result{IsError: true, Output: "unknown MCP server: " + in.Server}, nil
	}
	if cfg.URL == "" {
		return Result{IsError: true, Output: fmt.Sprintf("server %q uses a transport that does not support OAuth from this tool; run /mcp to authenticate manually.", in.Server)}, nil
	}

	oc := &mcp.OAuthConfig{
		ResourceURL: cfg.URL,
		Authorize:   mcp.BrowserAuthorizer,
		RedirectURI: "http://127.0.0.1:8765/callback",
		ClientName:  "bankai",
	}
	if _, err := oc.Authenticate(ctx); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("OAuth flow for %q failed: %v. Run /mcp to authenticate manually.", in.Server, err)}, nil
	}
	return Result{Output: fmt.Sprintf("Authenticated %q. Its tools will be available on the next MCP connection (restart bankai or reconnect).", in.Server)}, nil
}
