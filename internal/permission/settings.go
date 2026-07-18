package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// settingsFile is the subset of Claude Code's settings.json we read: the
// permissions.allow / permissions.deny rule lists and an optional defaultMode.
type settingsFile struct {
	Permissions struct {
		Allow       []string `json:"allow"`
		Deny        []string `json:"deny"`
		DefaultMode string   `json:"defaultMode"`
	} `json:"permissions"`
}

// LoadSettings reads permission rules from user (~/.claude/settings.json) and
// project (<projectDir>/.claude/settings.json) settings, plus the project-local
// override (settings.local.json). Later files' rules append to earlier ones;
// project rules take precedence at evaluation time only via deny>allow ordering,
// so we simply concatenate. A defaultMode found in any file (project wins) is
// returned; empty string means none specified.
func LoadSettings(homeDir, projectDir string) (allow, deny []Rule, mode Mode) {
	paths := []string{}
	if homeDir != "" {
		paths = append(paths, filepath.Join(homeDir, ".claude", "settings.json"))
	}
	if projectDir != "" {
		paths = append(paths,
			filepath.Join(projectDir, ".claude", "settings.json"),
			filepath.Join(projectDir, ".claude", "settings.local.json"),
		)
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var sf settingsFile
		if json.Unmarshal(raw, &sf) != nil {
			continue
		}
		for _, s := range sf.Permissions.Allow {
			if r, ok := parseRule(s, Allow); ok {
				allow = append(allow, r)
			}
		}
		for _, s := range sf.Permissions.Deny {
			if r, ok := parseRule(s, Deny); ok {
				deny = append(deny, r)
			}
		}
		if m := Mode(sf.Permissions.DefaultMode); m.Valid() {
			mode = m // later (project/local) files win
		}
	}
	return allow, deny, mode
}

// parseRule parses a Claude-Code-style rule string into a Rule. Forms:
//
//	"Bash"            -> {ToolName: "Bash"}
//	"Bash(git:*)"     -> {ToolName: "Bash", Match: "git:"}  (trailing * dropped)
//	"Read(/etc/**)"   -> {ToolName: "Read", Match: "/etc/"}
//
// The parenthesized content is treated as a substring match (glob wildcards are
// stripped to their literal prefix) — a pragmatic subset of Claude Code's
// matcher sufficient for gating.
func parseRule(s string, behavior Behavior) (Rule, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Rule{}, false
	}
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return Rule{ToolName: s, Behavior: behavior}, true
	}
	name := strings.TrimSpace(s[:open])
	if name == "" {
		return Rule{}, false
	}
	content := s
	if close := strings.LastIndexByte(s, ')'); close > open {
		content = s[open+1 : close]
	} else {
		content = s[open+1:]
	}
	// Reduce a glob to its literal leading segment: everything before the first
	// wildcard char becomes the required substring.
	if i := strings.IndexAny(content, "*?["); i >= 0 {
		content = content[:i]
	}
	// Claude Code uses ':' as the separator between a command and its arg glob
	// (e.g. "git status:*"); drop a trailing ':' so the literal prefix matches
	// the actual command text.
	content = strings.TrimRight(strings.TrimSpace(content), ":")
	content = strings.TrimSpace(content)
	return Rule{ToolName: name, Match: content, Behavior: behavior}, true
}
