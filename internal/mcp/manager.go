package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ServerConfig is one entry under settings.json "mcpServers". Two transports are
// supported: stdio (Command set) and streamable HTTP (Type "http"/"sse"/"streamable-http"
// with URL set). Entries matching neither are skipped.
type ServerConfig struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Auth    string            `json:"auth"` // "oauth" triggers the OAuth 2.1 flow
}

// isHTTP reports whether the config selects the HTTP transport.
func (c ServerConfig) isHTTP() bool {
	if c.URL == "" {
		return false
	}
	switch c.Type {
	case "http", "sse", "streamable-http", "streamableHttp":
		return true
	case "":
		return true // URL present, no explicit type → assume HTTP
	}
	return false
}

type settingsFile struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// LoadConfigs reads mcpServers from ~/.claude/settings.json and the project's
// .claude/settings.json (project entries override user entries by name).
func LoadConfigs(homeDir, projectDir string) map[string]ServerConfig {
	out := map[string]ServerConfig{}
	read := func(p string) {
		raw, err := os.ReadFile(p)
		if err != nil {
			return
		}
		var sf settingsFile
		if json.Unmarshal(raw, &sf) != nil {
			return
		}
		for name, cfg := range sf.MCPServers {
			out[name] = cfg
		}
	}
	if homeDir != "" {
		read(filepath.Join(homeDir, ".claude", "settings.json"))
	}
	if projectDir != "" {
		read(filepath.Join(projectDir, ".claude", "settings.json"))
	}
	return out
}

// Manager owns the live MCP client connections.
type Manager struct {
	clients   []*Client
	resources []BridgedResource
}

// BridgedResource is a resource advertised by a connected server.
type BridgedResource struct {
	Server string
	Info   ResourceInfo
	client *Client
}

// Resources returns all resources advertised by connected servers.
func (m *Manager) Resources() []BridgedResource { return m.resources }

// ReadResource reads a resource by URI from whichever server advertised it.
func (m *Manager) ReadResource(ctx context.Context, uri string) (string, bool, error) {
	for _, r := range m.resources {
		if r.Info.URI == uri {
			out, err := r.client.ReadResource(ctx, uri)
			return out, true, err
		}
	}
	return "", false, nil
}

// BridgedTool is a single MCP tool exposed for the agent to call.
type BridgedTool struct {
	Server        string
	QualifiedName string // mcp__<server>__<tool>
	Info          ToolInfo
	client        *Client
}

// Call invokes the underlying MCP tool.
func (t BridgedTool) Call(ctx context.Context, arguments json.RawMessage) (string, bool, error) {
	return t.client.CallTool(ctx, t.Info.Name, arguments)
}

// Start dials every configured stdio server and collects their tools. Servers
// that fail to start or list tools are skipped (their error is returned in the
// errs map keyed by server name) so one bad server does not block the rest.
func Start(ctx context.Context, configs map[string]ServerConfig) (*Manager, []BridgedTool, map[string]error) {
	m := &Manager{}
	var tools []BridgedTool
	errs := map[string]error{}

	names := make([]string, 0, len(configs))
	for n := range configs {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		cfg := configs[name]
		var c *Client
		var err error
		switch {
		case cfg.isHTTP():
			authHeader := cfg.Headers["Authorization"]
			if cfg.Auth == "oauth" && authHeader == "" {
				oc := &OAuthConfig{
					ResourceURL: cfg.URL,
					Authorize:   BrowserAuthorizer,
					RedirectURI: "http://127.0.0.1:8765/callback",
					ClientName:  "bankai",
				}
				tok, aerr := oc.Authenticate(ctx)
				if aerr != nil {
					errs[name] = fmt.Errorf("oauth: %w", aerr)
					continue
				}
				authHeader = tok.Header()
			}
			c, err = DialHTTP(ctx, name, cfg.URL, authHeader)
		case cfg.Command != "" && (cfg.Type == "" || cfg.Type == "stdio"):
			c, err = Dial(ctx, name, cfg.Command, cfg.Args, envSlice(cfg.Env))
		default:
			continue // unsupported transport
		}
		if err != nil {
			errs[name] = err
			continue
		}
		infos, err := c.ListTools(ctx)
		if err != nil {
			errs[name] = err
			c.Close()
			continue
		}
		m.clients = append(m.clients, c)
		// Resources are optional; a server without support just errors here.
		if res, rerr := c.ListResources(ctx); rerr == nil {
			for _, ri := range res {
				m.resources = append(m.resources, BridgedResource{Server: name, Info: ri, client: c})
			}
		}
		for _, info := range infos {
			tools = append(tools, BridgedTool{
				Server:        name,
				QualifiedName: "mcp__" + name + "__" + info.Name,
				Info:          info,
				client:        c,
			})
		}
	}
	return m, tools, errs
}

// Close shuts down every server.
func (m *Manager) Close() {
	for _, c := range m.clients {
		c.Close()
	}
}

// envSlice merges the current process env with per-server overrides.
func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := append([]string{}, os.Environ()...)
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
