// Package skills loads user- and project-scoped skills — packaged instruction
// sets the model can invoke on demand. It is a focused Go port of vibelearn's
// skills subsystem (src/skills/): each skill is a SKILL.md file with YAML
// frontmatter (name, description) plus a markdown body of instructions.
package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source describes where a skill was loaded from.
type Source string

const (
	SourceUser    Source = "user"    // ~/.claude/skills
	SourceProject Source = "project" // <cwd>/.claude/skills
)

// Skill is a single loaded skill.
type Skill struct {
	Name        string
	Description string
	Body        string
	Path        string
	Source      Source
}

// Set is an immutable collection of loaded skills keyed by name. Project skills
// override user skills of the same name.
type Set struct {
	byName map[string]Skill
}

// Load discovers skills in the user and project skill directories. Project
// skills take precedence over user skills with the same name. A missing
// directory is not an error.
func Load(homeDir, projectDir string) *Set {
	s := &Set{byName: map[string]Skill{}}
	// User first, then project so project overrides on name collision.
	if homeDir != "" {
		s.loadDir(filepath.Join(homeDir, ".claude", "skills"), SourceUser)
	}
	if projectDir != "" {
		s.loadDir(filepath.Join(projectDir, ".claude", "skills"), SourceProject)
	}
	return s
}

func (s *Set) loadDir(dir string, src Source) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sk := parse(string(raw))
		if sk.Name == "" {
			sk.Name = e.Name() // fall back to directory name
		}
		sk.Path = path
		sk.Source = src
		s.byName[sk.Name] = sk
	}
}

// Get returns a skill by name.
func (s *Set) Get(name string) (Skill, bool) {
	sk, ok := s.byName[name]
	return sk, ok
}

// List returns all skills sorted by name.
func (s *Set) List() []Skill {
	out := make([]Skill, 0, len(s.byName))
	for _, sk := range s.byName {
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Len reports how many skills are loaded.
func (s *Set) Len() int { return len(s.byName) }

// parse splits YAML frontmatter (name/description) from the markdown body. It
// is deliberately minimal: only top-level `key: value` lines are recognized.
func parse(raw string) Skill {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	var sk Skill
	if !strings.HasPrefix(raw, "---\n") {
		sk.Body = strings.TrimSpace(raw)
		return sk
	}
	rest := raw[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		sk.Body = strings.TrimSpace(raw)
		return sk
	}
	front := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")
	sk.Body = strings.TrimSpace(body)

	for _, line := range strings.Split(front, "\n") {
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		val = strings.Trim(val, `"'`)
		switch strings.ToLower(key) {
		case "name":
			sk.Name = val
		case "description":
			sk.Description = val
		}
	}
	return sk
}
