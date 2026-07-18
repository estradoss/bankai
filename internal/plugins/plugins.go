// Package plugins discovers and loads Claude-Code-style plugins from
// ~/.claude/plugins. It is a focused Go port of vibelearn's plugin subsystem
// (src/services/plugins/, src/utils/plugins/): each plugin is a directory with
// a plugin.json manifest that can contribute skills (skills/ subdir) and MCP
// servers (manifest mcpServers). Marketplace install/update and hooks/agents
// contribution are not yet ported.
package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/estradoss/bankai/internal/mcp"
)

// Manifest is the parsed plugin.json.
type Manifest struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Version     string                      `json:"version"`
	MCPServers  map[string]mcp.ServerConfig `json:"mcpServers"`
}

// Plugin is a discovered, loaded plugin.
type Plugin struct {
	Name        string
	Description string
	Version     string
	Dir         string
	SkillsDir   string                      // "" if the plugin has no skills/ dir
	MCPServers  map[string]mcp.ServerConfig // namespaced by caller if needed
}

// manifestPath returns the manifest location for a plugin dir, checking both
// the bare plugin.json and the .claude-plugin/plugin.json convention.
func manifestPath(dir string) string {
	for _, p := range []string{
		filepath.Join(dir, "plugin.json"),
		filepath.Join(dir, ".claude-plugin", "plugin.json"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Load discovers plugins under <homeDir>/.claude/plugins. Each immediate
// subdirectory with a manifest becomes a Plugin. Disabled names (from settings)
// are skipped. Missing plugins dir yields nil. Results are sorted by name.
func Load(homeDir string, disabled map[string]bool) []Plugin {
	if homeDir == "" {
		return nil
	}
	root := filepath.Join(homeDir, ".claude", "plugins")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Plugin
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		mp := manifestPath(dir)
		if mp == "" {
			continue
		}
		raw, err := os.ReadFile(mp)
		if err != nil {
			continue
		}
		var man Manifest
		if json.Unmarshal(raw, &man) != nil {
			continue
		}
		if man.Name == "" {
			man.Name = e.Name()
		}
		if disabled[man.Name] {
			continue
		}
		p := Plugin{
			Name:        man.Name,
			Description: man.Description,
			Version:     man.Version,
			Dir:         dir,
			MCPServers:  man.MCPServers,
		}
		skdir := filepath.Join(dir, "skills")
		if fi, err := os.Stat(skdir); err == nil && fi.IsDir() {
			p.SkillsDir = skdir
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CollectMCPServers merges every plugin's MCP servers into one map, prefixing
// each server name with the plugin name to avoid collisions.
func CollectMCPServers(ps []Plugin) map[string]mcp.ServerConfig {
	out := map[string]mcp.ServerConfig{}
	for _, p := range ps {
		for name, cfg := range p.MCPServers {
			out[p.Name+":"+name] = cfg
		}
	}
	return out
}
