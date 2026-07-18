package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type GrepTool struct{}

func (GrepTool) Name() string { return "Grep" }

func (GrepTool) Description() string {
	return "Search file contents with a regular expression. Uses ripgrep when available, otherwise a built-in scanner. Options: `path` (dir/file to search, default cwd), `glob` (filter files, e.g. *.go), `output_mode` (files_with_matches|content|count, default files_with_matches), `-i` (case-insensitive), `-n` (show line numbers, content mode)."
}

func (GrepTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Regular expression to search for"},
			"path": {"type": "string", "description": "File or directory to search (default cwd)"},
			"glob": {"type": "string", "description": "Filter files by glob, e.g. *.go"},
			"output_mode": {"type": "string", "enum": ["files_with_matches", "content", "count"], "description": "Result shape (default files_with_matches)"},
			"-i": {"type": "boolean", "description": "Case-insensitive"},
			"-n": {"type": "boolean", "description": "Show line numbers (content mode)"}
		},
		"required": ["pattern"]
	}`)
}

type grepInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	OutputMode string `json:"output_mode"`
	IgnoreCase bool   `json:"-i"`
	LineNums   bool   `json:"-n"`
}

func (GrepTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in grepInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Pattern == "" {
		return Result{IsError: true, Output: "pattern is required"}, nil
	}
	if in.Path == "" {
		in.Path, _ = os.Getwd()
	}
	if in.OutputMode == "" {
		in.OutputMode = "files_with_matches"
	}

	if _, err := exec.LookPath("rg"); err == nil {
		if out, ok := grepRipgrep(ctx, in); ok {
			return out, nil
		}
	}
	return grepGo(in)
}

func grepRipgrep(ctx context.Context, in grepInput) (Result, bool) {
	args := []string{"--color=never"}
	if in.IgnoreCase {
		args = append(args, "-i")
	}
	switch in.OutputMode {
	case "files_with_matches":
		args = append(args, "-l")
	case "count":
		args = append(args, "-c")
	case "content":
		if in.LineNums {
			args = append(args, "-n")
		}
	}
	if in.Glob != "" {
		args = append(args, "-g", in.Glob)
	}
	args = append(args, "-e", in.Pattern, in.Path)
	cmd := exec.CommandContext(ctx, "rg", args...)
	out, err := cmd.CombinedOutput()
	// rg exits 1 when no matches — treat as empty, not error.
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return Result{Output: "No matches found."}, true
		}
		return Result{}, false
	}
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return Result{Output: "No matches found."}, true
	}
	return Result{Output: s}, true
}

func grepGo(in grepInput) (Result, error) {
	pat := in.Pattern
	if in.IgnoreCase {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad regexp: %v", err)}, nil
	}

	var files []string
	fi, err := os.Stat(in.Path)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	if fi.IsDir() {
		_ = filepath.WalkDir(in.Path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				n := d.Name()
				if n == ".git" || n == "node_modules" || n == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if in.Glob != "" {
				if ok, _ := filepath.Match(in.Glob, d.Name()); !ok {
					return nil
				}
			}
			files = append(files, p)
			return nil
		})
	} else {
		files = []string{in.Path}
	}

	var matchedFiles []string
	counts := map[string]int{}
	var content strings.Builder
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		scan := bufio.NewScanner(fh)
		scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		lineNo := 0
		matchedThis := false
		for scan.Scan() {
			lineNo++
			line := scan.Text()
			if re.MatchString(line) {
				matchedThis = true
				counts[f]++
				if in.OutputMode == "content" {
					if in.LineNums {
						fmt.Fprintf(&content, "%s:%d:%s\n", f, lineNo, line)
					} else {
						fmt.Fprintf(&content, "%s:%s\n", f, line)
					}
				}
			}
		}
		fh.Close()
		if matchedThis {
			matchedFiles = append(matchedFiles, f)
		}
	}

	switch in.OutputMode {
	case "content":
		s := strings.TrimRight(content.String(), "\n")
		if s == "" {
			return Result{Output: "No matches found."}, nil
		}
		return Result{Output: s}, nil
	case "count":
		if len(counts) == 0 {
			return Result{Output: "No matches found."}, nil
		}
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "%s:%d\n", k, counts[k])
		}
		return Result{Output: strings.TrimRight(b.String(), "\n")}, nil
	default:
		if len(matchedFiles) == 0 {
			return Result{Output: "No matches found."}, nil
		}
		sort.Strings(matchedFiles)
		return Result{Output: strings.Join(matchedFiles, "\n")}, nil
	}
}
