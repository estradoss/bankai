// Package memory is a file-based persistent memory store, a Go port of
// vibelearn's memdir subsystem (src/memdir/, src/services/SessionMemory/). Each
// memory is one markdown file with frontmatter (name, description, type) plus a
// body; a MEMORY.md index lists one pointer line per memory. Memories capture
// context NOT derivable from project state (user profile, feedback, project
// notes, external references).
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Type is the memory taxonomy.
type Type string

const (
	TypeUser      Type = "user"
	TypeFeedback  Type = "feedback"
	TypeProject   Type = "project"
	TypeReference Type = "reference"
)

// ParseType returns the Type for a raw frontmatter value, or "" if unknown.
func ParseType(raw string) Type {
	switch Type(strings.TrimSpace(raw)) {
	case TypeUser:
		return TypeUser
	case TypeFeedback:
		return TypeFeedback
	case TypeProject:
		return TypeProject
	case TypeReference:
		return TypeReference
	}
	return ""
}

// Memory is a single stored memory.
type Memory struct {
	Name        string
	Description string
	Type        Type
	Body        string
	Path        string
}

// Store is a directory of memory files with a MEMORY.md index.
type Store struct {
	dir string
}

// NewStore roots a store at dir (created lazily on first Save).
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir returns the store's directory.
func (s *Store) Dir() string { return s.dir }

func slugify(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	prevDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// List loads all memories (excluding the MEMORY.md index), sorted by name.
func (s *Store) List() []Memory {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []Memory
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "MEMORY.md" {
			continue
		}
		path := filepath.Join(s.dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		m := parse(string(raw))
		if m.Name == "" {
			m.Name = strings.TrimSuffix(e.Name(), ".md")
		}
		m.Path = path
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a memory by name.
func (s *Store) Get(name string) (Memory, bool) {
	for _, m := range s.List() {
		if m.Name == name {
			return m, true
		}
	}
	return Memory{}, false
}

// Save writes (or overwrites) a memory and refreshes the MEMORY.md index. The
// name is slugified for the filename; an empty type defaults to project.
func (s *Store) Save(m Memory) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("memory name is required")
	}
	if m.Type == "" {
		m.Type = TypeProject
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	slug := slugify(m.Name)
	if slug == "" {
		return fmt.Errorf("memory name %q slugifies to empty", m.Name)
	}
	m.Name = slug
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\nmetadata:\n  type: %s\n---\n\n%s\n",
		m.Name, m.Description, m.Type, strings.TrimSpace(m.Body))
	if err := os.WriteFile(filepath.Join(s.dir, slug+".md"), []byte(content), 0o644); err != nil {
		return err
	}
	return s.writeIndex()
}

// Delete removes a memory by name and refreshes the index. Missing is not error.
func (s *Store) Delete(name string) error {
	_ = os.Remove(filepath.Join(s.dir, slugify(name)+".md"))
	return s.writeIndex()
}

// writeIndex regenerates MEMORY.md — one pointer line per memory.
func (s *Store) writeIndex() error {
	mems := s.List()
	var b strings.Builder
	b.WriteString("# Memory index\n\n")
	for _, m := range mems {
		hook := m.Description
		if hook == "" {
			hook = string(m.Type)
		}
		fmt.Fprintf(&b, "- [%s](%s.md) — %s\n", m.Name, m.Name, hook)
	}
	if len(mems) == 0 {
		return os.Remove(filepath.Join(s.dir, "MEMORY.md"))
	}
	return os.WriteFile(filepath.Join(s.dir, "MEMORY.md"), []byte(b.String()), 0o644)
}

// Index returns the MEMORY.md contents (empty string if none), for injecting
// the memory pointer list into a session's context.
func (s *Store) Index() string {
	raw, err := os.ReadFile(filepath.Join(s.dir, "MEMORY.md"))
	if err != nil {
		return ""
	}
	return string(raw)
}

// FindRelevant scores memories against a free-text query by counting query-word
// occurrences in each memory's name/description/body, returning the top n
// (score > 0), most relevant first.
func (s *Store) FindRelevant(query string, n int) []Memory {
	words := tokenize(query)
	if len(words) == 0 {
		return nil
	}
	type scored struct {
		m Memory
		s int
	}
	var ranked []scored
	for _, m := range s.List() {
		hay := strings.ToLower(m.Name + " " + m.Description + " " + m.Body)
		score := 0
		for w := range words {
			score += strings.Count(hay, w)
		}
		if score > 0 {
			ranked = append(ranked, scored{m, score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].s > ranked[j].s })
	if n > 0 && len(ranked) > n {
		ranked = ranked[:n]
	}
	out := make([]Memory, len(ranked))
	for i, r := range ranked {
		out[i] = r.m
	}
	return out
}

func tokenize(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(w) >= 3 { // skip trivially short tokens
			out[w] = struct{}{}
		}
	}
	return out
}

// parse splits frontmatter (name/description/metadata.type) from the body.
func parse(raw string) Memory {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	var m Memory
	if !strings.HasPrefix(raw, "---\n") {
		m.Body = strings.TrimSpace(raw)
		return m
	}
	rest := raw[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		m.Body = strings.TrimSpace(raw)
		return m
	}
	front := rest[:end]
	m.Body = strings.TrimPrefix(rest[end+len("\n---"):], "\n")
	m.Body = strings.TrimSpace(m.Body)
	for _, line := range strings.Split(front, "\n") {
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.Trim(strings.TrimSpace(line[colon+1:]), `"'`)
		switch strings.ToLower(key) {
		case "name":
			m.Name = val
		case "description":
			m.Description = val
		case "type":
			if t := ParseType(val); t != "" {
				m.Type = t
			}
		}
	}
	return m
}
