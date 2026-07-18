package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type EditTool struct{}

func (EditTool) Name() string { return "Edit" }

func (EditTool) Description() string {
	return "Replace exact old_string with new_string in a file. old_string must occur exactly once unless replace_all=true. Absolute paths only."
}

func (EditTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file_path":   {"type": "string"},
			"old_string":  {"type": "string"},
			"new_string":  {"type": "string"},
			"replace_all": {"type": "boolean"}
		},
		"required": ["file_path", "old_string", "new_string"]
	}`)
}

func (EditTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		FilePath   string `json:"file_path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if !filepath.IsAbs(in.FilePath) {
		return Result{IsError: true, Output: "file_path must be absolute"}, nil
	}
	if in.OldString == in.NewString {
		return Result{IsError: true, Output: "old_string and new_string are identical"}, nil
	}
	b, err := os.ReadFile(in.FilePath)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	src := string(b)
	var out string
	if in.ReplaceAll {
		out = strings.ReplaceAll(src, in.OldString, in.NewString)
		if out == src {
			return Result{IsError: true, Output: "old_string not found"}, nil
		}
	} else {
		n := strings.Count(src, in.OldString)
		if n == 0 {
			return Result{IsError: true, Output: "old_string not found"}, nil
		}
		if n > 1 {
			return Result{IsError: true, Output: fmt.Sprintf("old_string matches %d times; make it unique or set replace_all=true", n)}, nil
		}
		out = strings.Replace(src, in.OldString, in.NewString, 1)
	}
	if err := os.WriteFile(in.FilePath, []byte(out), 0o644); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("edited %s", in.FilePath)}, nil
}
