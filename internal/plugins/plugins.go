// Package plugins discovers and loads Claude-Code-style plugins from
// ~/.claude/plugins. It is a focused Go port of vibelearn's plugin subsystem
// (src/services/plugins/, src/utils/plugins/): each plugin is a directory with
// a plugin.json manifest that can contribute skills (skills/ subdir), MCP
// servers (manifest mcpServers), sub-agent types (manifest agents), and hooks
// (manifest hooks). Marketplace install/update/remove is git-based (Install/
// Update/Remove).
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/estradoss/bankai/internal/mcp"
)

// AgentDef is a plugin-contributed sub-agent type.
type AgentDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
}

// HookDef is one plugin-contributed hook: run Command when a tool matching
// Matcher (a regexp on the tool name) fires the Event (e.g. "PostToolUse").
type HookDef struct {
	Event   string `json:"event"`
	Matcher string `json:"matcher"`
	Command string `json:"command"`
}

// Manifest is the parsed plugin.json.
type Manifest struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Version     string                      `json:"version"`
	MCPServers  map[string]mcp.ServerConfig `json:"mcpServers"`
	Agents      []AgentDef                  `json:"agents"`
	Hooks       []HookDef                   `json:"hooks"`
}

// Plugin is a discovered, loaded plugin.
type Plugin struct {
	Name        string
	Description string
	Version     string
	Dir         string
	SkillsDir   string                      // "" if the plugin has no skills/ dir
	MCPServers  map[string]mcp.ServerConfig // namespaced by caller if needed
	Agents      []AgentDef
	Hooks       []HookDef
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
			Agents:      man.Agents,
			Hooks:       man.Hooks,
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

// CollectAgents merges every plugin's agent definitions, namespaced by plugin
// name to avoid collisions (name form "<plugin>:<agent>").
func CollectAgents(ps []Plugin) []AgentDef {
	var out []AgentDef
	for _, p := range ps {
		for _, a := range p.Agents {
			if a.Name == "" {
				continue
			}
			out = append(out, AgentDef{Name: p.Name + ":" + a.Name, Description: a.Description, Prompt: a.Prompt})
		}
	}
	return out
}

// CollectHooks merges every plugin's hook definitions in load order.
func CollectHooks(ps []Plugin) []HookDef {
	var out []HookDef
	for _, p := range ps {
		out = append(out, p.Hooks...)
	}
	return out
}

// pluginsDir returns the plugins root under homeDir.
func pluginsDir(homeDir string) string {
	return filepath.Join(homeDir, ".claude", "plugins")
}

// deriveName extracts a plugin directory name from a git URL
// (github.com/foo/bar.git → bar).
func deriveName(url string) string {
	s := strings.TrimSuffix(url, ".git")
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// Install clones a plugin from a git URL into ~/.claude/plugins. Returns the
// installed plugin's directory name. Ported from vibelearn's marketplace install.
func Install(homeDir, gitURL string) (string, error) {
	name := deriveName(gitURL)
	if name == "" {
		return "", fmt.Errorf("could not derive plugin name from %q", gitURL)
	}
	root := pluginsDir(homeDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(root, name)
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("plugin %q already installed at %s (use Update)", name, dest)
	}
	out, err := runGit(root, "clone", "--depth", "1", gitURL, name)
	if err != nil {
		return "", fmt.Errorf("git clone failed: %s", out)
	}
	if manifestPath(dest) == "" {
		_ = os.RemoveAll(dest)
		return "", fmt.Errorf("%q has no plugin.json manifest; not a plugin", gitURL)
	}
	return name, nil
}

// Update runs `git pull` in an installed plugin's directory.
func Update(homeDir, name string) (string, error) {
	dest := filepath.Join(pluginsDir(homeDir), name)
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		return "", fmt.Errorf("plugin %q is not a git checkout", name)
	}
	out, err := runGit(dest, "pull", "--ff-only")
	if err != nil {
		return "", fmt.Errorf("git pull failed: %s", out)
	}
	return strings.TrimSpace(out), nil
}

// Remove deletes an installed plugin directory.
func Remove(homeDir, name string) error {
	dest := filepath.Join(pluginsDir(homeDir), name)
	if _, err := os.Stat(dest); err != nil {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	return os.RemoveAll(dest)
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
