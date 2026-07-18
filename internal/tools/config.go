package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigTool gets or sets bankai/Claude-Code configuration in settings.json.
// Ported (pragmatically) from vibelearn's ConfigTool: omit "value" to read a
// setting, include it to write. Keys are dotted paths (e.g.
// "permissions.defaultMode"). Writes go to the user settings file
// (~/.claude/settings.json) unless scope="project" (.claude/settings.json).
// Note: most settings take effect on the next session start.
type ConfigTool struct {
	HomeDir    string
	ProjectDir string
}

func (ConfigTool) Name() string { return "Config" }

func (ConfigTool) Description() string {
	return "Get or set bankai configuration settings in settings.json. Omit \"value\" to read the current " +
		"value; include it to set. \"setting\" is a dotted key (e.g. \"model\", \"permissions.defaultMode\"). " +
		"scope is \"user\" (default, ~/.claude/settings.json) or \"project\" (.claude/settings.json). " +
		"Most settings apply on the next session start."
}

func (ConfigTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"setting": {"type": "string", "description": "Dotted setting key, e.g. permissions.defaultMode"},
			"value": {"description": "New value (string/number/bool). Omit to read the current value."},
			"scope": {"type": "string", "enum": ["user", "project"], "description": "Which settings file (default user)"}
		},
		"required": ["setting"]
	}`)
}

func (t ConfigTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Setting string          `json:"setting"`
		Value   json.RawMessage `json:"value"`
		Scope   string          `json:"scope"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if strings.TrimSpace(in.Setting) == "" {
		return Result{IsError: true, Output: "setting is required"}, nil
	}
	path, err := t.settingsPath(in.Scope)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}

	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return Result{IsError: true, Output: fmt.Sprintf("existing %s is not valid JSON: %v", path, err)}, nil
		}
	}
	keys := strings.Split(in.Setting, ".")

	// Read (no value provided).
	if len(in.Value) == 0 {
		v, ok := getPath(settings, keys)
		if !ok {
			return Result{Output: fmt.Sprintf("%s is not set in %s", in.Setting, path)}, nil
		}
		out, _ := json.Marshal(v)
		return Result{Output: fmt.Sprintf("%s = %s (%s)", in.Setting, out, path)}, nil
	}

	// Write.
	var val any
	if err := json.Unmarshal(in.Value, &val); err != nil {
		val = string(in.Value)
	}
	setPath(settings, keys, val)
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	shown, _ := json.Marshal(val)
	return Result{Output: fmt.Sprintf("set %s = %s in %s (applies on next session start)", in.Setting, shown, path)}, nil
}

func (t ConfigTool) settingsPath(scope string) (string, error) {
	if scope == "project" {
		dir := t.ProjectDir
		if dir == "" {
			d, err := os.Getwd()
			if err != nil {
				return "", err
			}
			dir = d
		}
		return filepath.Join(dir, ".claude", "settings.json"), nil
	}
	home := t.HomeDir
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func getPath(m map[string]any, keys []string) (any, bool) {
	cur := any(m)
	for _, k := range keys {
		mp, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mp[k]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setPath(m map[string]any, keys []string, val any) {
	cur := m
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	cur[keys[len(keys)-1]] = val
}
