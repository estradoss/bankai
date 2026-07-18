package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type GlobTool struct{}

func (GlobTool) Name() string { return "Glob" }

func (GlobTool) Description() string {
	return "Fast file-pattern matching. Supports glob patterns like **/*.go or src/**/*.ts (** matches any number of directories). Returns matching absolute paths sorted by modification time (newest first). Use `path` to scope the search to a directory (defaults to cwd)."
}

func (GlobTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Glob pattern, e.g. **/*.go"},
			"path": {"type": "string", "description": "Directory to search in (default: cwd)"}
		},
		"required": ["pattern"]
	}`)
}

func (GlobTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Pattern == "" {
		return Result{IsError: true, Output: "pattern is required"}, nil
	}
	base := in.Path
	if base == "" {
		base, _ = os.Getwd()
	}
	if !filepath.IsAbs(base) {
		return Result{IsError: true, Output: "path must be absolute"}, nil
	}

	type ent struct {
		path string
		mod  int64
	}
	var ents []ent
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if matchGlob(in.Pattern, rel) {
			fi, ferr := d.Info()
			if ferr == nil {
				ents = append(ents, ent{p, fi.ModTime().UnixNano()})
			}
		}
		return nil
	})
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].mod > ents[j].mod })
	if len(ents) == 0 {
		return Result{Output: "No files matched."}, nil
	}
	var b strings.Builder
	for _, e := range ents {
		b.WriteString(e.path)
		b.WriteByte('\n')
	}
	return Result{Output: b.String()}, nil
}

// matchGlob matches a slash-separated path against a glob pattern that may
// contain ** (match across directory separators). Single * and ? are matched
// per-segment via path.Match.
func matchGlob(pattern, name string) bool {
	// Fast path: no ** — use path.Match against the full path if pattern has
	// slashes, else against the basename.
	if !strings.Contains(pattern, "**") {
		if strings.Contains(pattern, "/") {
			ok, _ := path.Match(pattern, name)
			return ok
		}
		ok, _ := path.Match(pattern, path.Base(name))
		return ok
	}
	return matchDoubleStar(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchDoubleStar(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Collapse consecutive **.
			for len(pat) > 0 && pat[0] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 0 {
				return true // trailing ** matches everything
			}
			// Try to match the rest at every suffix of name.
			for i := 0; i <= len(name); i++ {
				if matchDoubleStar(pat, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
